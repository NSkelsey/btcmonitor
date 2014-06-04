package main

import (
	"fmt"
	"log"
	"math/rand"
	"net"
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

func connHandler(id int, outAddrs chan<- []*btc.NetAddress, outNode chan<- Node, inAddr <-chan *btc.NetAddress) {
	// A worker that deals with the connection to a single bitcoin node,
	// writes the list of nodes reported by node into out
	// writes a valid node into outNode

	for {
		addr := <-inAddr

		strA := addressFmt(*addr)
		conn, err := net.Dial("tcp", strA)
		log.Printf("[%d] Dialed: %s\n", id, strA)
		if err != nil {
			log.Printf("FAILED: %s: %s\n", strA, err)
			break
		}

		ver_m, _ := btc.NewMsgVersionFromConn(conn, genNonce(), 258823)
		ver_m.AddUserAgent("nodesearch", "0.0.1")
		write(conn, ver_m)

		time.AfterFunc(time.Second*6, func() { conn.Close() })
		for {
			var resp btc.Message
			resp, _, err := btc.ReadMessage(conn, pver, btcnet)
			if err != nil {
				log.Printf("FAILED: %s: %s\n", strA, err)
				break
			}
			log.Println(resp.Command())
			switch resp := resp.(type) {
			case *btc.MsgVersion:
				node := conv_to_node(*addr, *resp)
				outNode <- node
				verack := btc.NewMsgVerAck()
				write(conn, verack)
				getAddr := btc.NewMsgGetAddr()
				write(conn, getAddr)
			case *btc.MsgAddr:
				addrs := resp.AddrList
				log.Println(len(addrs))
				outAddrs <- addrs
				break
			case *btc.MsgPing:
				nonce := resp.Nonce
				pong := btc.NewMsgPong(nonce)
				write(conn, pong)
			}
		}
	}
}

func main() {

	var addrMap = make(map[string]Node)

	// Multiplex writes into single channel
	var incomingAddrs = make(chan []*btc.NetAddress, 1000)
	var outgoingAddr = make(chan *btc.NetAddress, 10000)
	var liveNodes = make(chan Node)

	for i := 0; i < 50; i += 1 {
		go connHandler(i, incomingAddrs, liveNodes, outgoingAddr)
	}

	timer := time.NewTimer(time.Second * 30)
	var visited = make(map[string]bool)
	var addrs []*btc.NetAddress
	var node Node
	cnt := 0

	// Initial connection into net
	ip, port := "54.83.28.75", uint16(18333)
	home := btc.NetAddress{time.Now(), *new(btc.ServiceFlag), net.ParseIP(ip), port}
	// Give first goroutine something to do :)
	outgoingAddr <- &home

L3:
	for {
		select {
		case addrs = <-incomingAddrs:
			//fmt.Println(len(addrs))
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
			i := 0
			fmt.Printf("Run Summary:\nNodes alive: %d\nNodes buffered: %d\nNodes queued: %d\n", len(addrMap), cnt, i)
			break L3
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
