package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	btc "github.com/conformal/btcwire"
)

var btcnet = btc.TestNet3

type Node struct {
	Addr      btc.NetAddress
	Version   int32
	UserAgent string
	Services  btc.ServiceFlag
}

type LogLevel int

const (
	Error LogLevel = iota
	Warn
	Info
	Log
)

var Level = Info
var logger = log.New(os.Stdout, "", log.Ltime)

var outFile = flag.String("o", "run.json", "File to dump json into")
var runTime = flag.Int("runtime", 60, "The runtime of the script")
var bootstrap = flag.String("host", "127.0.0.1:18333", "The initial machine to bootstrap into the network")
var mainnetFlag = flag.Bool("mainnet", false, "Use Mainnet?")

func connHandler(id int, outAddrs chan<- []*btc.NetAddress, outNode chan<- Node, inAddr <-chan *btc.NetAddress) {
	// A worker that deals with the connection to a single bitcoin node.
	// It writes the list of nodes reported by node into out.
	// It also writes a valid node into outNode.
	// It reads from inAddr everytime it closes a connection

	for {
		addr := <-inAddr
		strA := addressFmt(*addr)

		threadLog := func(reported LogLevel, msg string) {
			if reported <= Level {
				logger.Printf("[%d] %s: %s\n", id, strA, msg)
			}
		}
		connProtoVer := btc.ProtocolVersion
		write := composeWrite(threadLog, connProtoVer)

		conn, err := net.DialTimeout("tcp", strA, time.Millisecond*500)
		if err != nil {
			threadLog(Log, err.Error())
			continue
		}
		threadLog(Info, "Connected")

		ver_m, _ := btc.NewMsgVersionFromConn(conn, genNonce(), 0)
		ver_m.AddUserAgent("btcmonitor", "0.0.1")
		write(conn, ver_m)

		// We are looking for successful addr messages
		wins := 0
		// After 10 seconds we just close the conn and handle errors
		time.AfterFunc(time.Second*10, func() { conn.Close() })
	MessageLoop:
		for {
			var resp btc.Message
			resp, _, err := btc.ReadMessage(conn, connProtoVer, btcnet)
			if err != nil {
				threadLog(Log, err.Error())
				break MessageLoop
			}
			threadLog(Info, resp.Command())
			switch resp := resp.(type) {
			case *btc.MsgVersion:
				nodePVer := uint32(resp.ProtocolVersion)
				if nodePVer < connProtoVer {
					connProtoVer = nodePVer
					write = composeWrite(threadLog, connProtoVer)
				}
				node := convNode(*addr, *resp)
				outNode <- node
				verack := btc.NewMsgVerAck()
				write(conn, verack)
				getAddr := btc.NewMsgGetAddr()
				write(conn, getAddr)
			case *btc.MsgAddr:
				wins += 1
				addrs := resp.AddrList
				outAddrs <- addrs
				if wins == 3 {
					break MessageLoop
				}
			case *btc.MsgPing:
				nonce := resp.Nonce
				pong := btc.NewMsgPong(nonce)
				write(conn, pong)
			}
		}
	}
}

func init() {
	flag.BoolVar(mainnetFlag, "M", false, "")
	flag.IntVar(runTime, "r", 60, "")
	flag.Usage = usage
}

func main() {
	flag.Parse()
	if *mainnetFlag {
		btcnet = btc.MainNet
	}

	var addrMap = make(map[string]Node)

	numWorkers := 250
	// Multiplex writes into single channel
	var incomingAddrs = make(chan []*btc.NetAddress)
	var outgoingAddr = make(chan *btc.NetAddress, 5e5)
	var liveNodes = make(chan Node)

	for i := 0; i < numWorkers; i += 1 {
		go connHandler(i, incomingAddrs, liveNodes, outgoingAddr)
	}

	rt := time.Duration(*runTime)
	timer := time.NewTimer(time.Second * rt)
	var visited = make(map[string]struct{})
	var addrs []*btc.NetAddress
	var node Node
	cnt := 0

	// Initial connection into net
	pair := strings.Split(*bootstrap, ":")
	ip := pair[0]
	port, _ := strconv.ParseUint(pair[1], 10, 16)
	home := btc.NewNetAddressIPPort(net.ParseIP(ip), uint16(port), 0)
	// Give first goroutine something to do :)
	outgoingAddr <- home

MainLoop:
	for {
		// This select statement does one of 3 things:
		// [1] Receives lists of addresses to search and hands them off to connection workers
		// [2] Receives responding nodes from child workers
		// [3] Times out execution of the script and cleans up
		select {
		case addrs = <-incomingAddrs:
			for i := range addrs {
				addr := addrs[i]
				key := addressFmt(*addr)
				if _, ok := visited[key]; !ok {
					cnt += 1
					outgoingAddr <- addr
				}
				// empty struct
				visited[key] = struct{}{}
			}
		case node = <-liveNodes:
			addrMap[key(node)] = node
		case <-timer.C:
			fmt.Printf("Run Summary:\nNodes responding: %d\nNodes buffered: %d\nNodes visited: %d\n", len(addrMap), cnt, len(visited))
			outs := ""
			for addrStr, node := range addrMap {
				outs += addrStr + " " + node.UserAgent + "\n"
			}
			ioutil.WriteFile(*outFile, []byte(outs), 0644)
			break MainLoop
		}
	}
}

// utility functions
func addressFmt(addr btc.NetAddress) string {

	return addr.IP.String() + ":" + strconv.Itoa(int(addr.Port))
}

func key(node Node) string {
	return addressFmt(node.Addr)
}

func composeWrite(threadLog func(LogLevel, string), pver uint32) func(net.Conn, btc.Message) {
	// creates a write function with logging in a closure
	return func(conn net.Conn, msg btc.Message) {
		err := btc.WriteMessage(conn, msg, pver, btcnet)
		if err != nil {
			threadLog(Info, err.Error())
		}
	}
}

func convNode(addr btc.NetAddress, ver btc.MsgVersion) Node {
	n := Node{
		Addr:      addr,
		Version:   ver.ProtocolVersion,
		UserAgent: ver.UserAgent,
		Services:  ver.Services,
	}
	return n
}

func genNonce() uint64 {
	n, _ := btc.RandomUint64()
	return n
}

func usage() {
	fmt.Fprintf(os.Stderr, "usage: span [options]\n")
	flag.PrintDefaults()
	os.Exit(2)
}
