package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net"
	"os"
	"strconv"
	"time"

	btc "github.com/conformal/btcwire"
)

var btcnet = btc.TestNet3
var pver = btc.ProtocolVersion
var debug = false

type Node struct {
	Addr      btc.NetAddress
	Version   int32
	UserAgent string
	Services  btc.ServiceFlag
}

type LogLevel int

const (
	ERROR LogLevel = iota
	WARN
	INFO
	LOG
)

var LEVEL = INFO
var logger = log.New(os.Stdout, "", log.Ltime)

var outFlag = flag.String("o", "run.json", "File to dump json into")
var runTime = flag.Int("runtime", 60, "The runtime of the script")

func connHandler(id int, outAddrs chan<- []*btc.NetAddress, outNode chan<- Node, inAddr <-chan *btc.NetAddress) {
	// A worker that deals with the connection to a single bitcoin node.
	// It writes the list of nodes reported by node into out.
	// It also writes a valid node into outNode.
	// It reads from inAddr everytime it closes a connection

	for {
		addr := <-inAddr
		strA := addressFmt(*addr)

		threadLog := func(level LogLevel, msg string) {
			if level <= LEVEL {
				logger.Printf("[%d] %s: %s\n", id, strA, msg)
			}
		}

		conn, err := net.DialTimeout("tcp", strA, time.Millisecond*500)
		if err != nil {
			threadLog(LOG, err.Error())
			continue
		}
		threadLog(INFO, "Connected")

		ver_m, _ := btc.NewMsgVersionFromConn(conn, genNonce(), 258823)
		ver_m.AddUserAgent("btcmonitor", "0.0.1")
		write(conn, ver_m)

		// We are looking successful addr messages
		wins := 0
		time.AfterFunc(time.Second*6, func() { conn.Close() })
	MessageLoop:
		for {
			var resp btc.Message
			resp, _, err := btc.ReadMessage(conn, pver, btcnet)
			if err != nil {
				threadLog(LOG, err.Error())
				break MessageLoop
			}
			threadLog(INFO, resp.Command())
			switch resp := resp.(type) {
			case *btc.MsgVersion:
				node := conv_to_node(*addr, *resp)
				outNode <- node
				verack := btc.NewMsgVerAck()
				write(conn, verack)
				getAddr := btc.NewMsgGetAddr()
				write(conn, getAddr)
			case *btc.MsgAddr:
				wins += 1
				addrs := resp.AddrList
				log.Println(len(addrs))
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
	flag.IntVar(runTime, "r", 60, "")
	flag.Usage = usage
}

func main() {
	flag.Parse()

	var addrMap = make(map[string]Node)

	// Multiplex writes into single channel
	var incomingAddrs = make(chan []*btc.NetAddress, 1000)
	var outgoingAddr = make(chan *btc.NetAddress, 10000)
	var liveNodes = make(chan Node)

	for i := 0; i < 150; i += 1 {
		go connHandler(i, incomingAddrs, liveNodes, outgoingAddr)
	}

	rt := time.Duration(*runTime)
	timer := time.NewTimer(time.Second * rt)
	var visited = make(map[string]bool)
	var addrs []*btc.NetAddress
	var node Node
	cnt := 0

	// Initial connection into net
	ip, port := "127.0.0.1", uint16(18333)
	home := btc.NetAddress{time.Now(), *new(btc.ServiceFlag), net.ParseIP(ip), port}
	// Give first goroutine something to do :)
	outgoingAddr <- &home

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
				visited[key] = true
			}
		case node = <-liveNodes:
			addrMap[key(node)] = node
		case <-timer.C:
			close(outgoingAddr)
			fmt.Printf("Run Summary:\nNodes responding: %d\nNodes buffered: %d\nNodes visited: %d\n", len(addrMap), cnt, len(visited))
			outs := ""
			for addrStr, node := range addrMap {
				outs += addrStr + " " + node.UserAgent + "\n"
			}
			ioutil.WriteFile(*outFlag, []byte(outs), 0644)
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

func write(conn net.Conn, msg btc.Message) {
	var btcnet = btc.TestNet3
	var pver = btc.ProtocolVersion
	err := btc.WriteMessage(conn, msg, pver, btcnet)
	if err != nil {
		println("Could not write for reason: ", err)
	}
}

func conv_to_node(addr btc.NetAddress, ver btc.MsgVersion) Node {
	n := Node{addr,
		ver.ProtocolVersion,
		ver.UserAgent,
		ver.Services,
	}
	return n
}

func genNonce() uint64 {
	return uint64(rand.Int63())
}

func usage() {
	fmt.Fprintf(os.Stderr, "usage: span [filename]\n")
	flag.PrintDefaults()
	os.Exit(2)
}
