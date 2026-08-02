// tick_sub: subscribe to moomoo OpenD TICKER pushes, print each tick + 1s summary.
//
// Usage: go run ./cmd/tick_sub [SYMBOL]
//
//	SYMBOL defaults to US.BNRG (also accepts bare "BNRG" → US.BNRG)
package main

import (
	"bufio"
	"crypto/sha1"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/common"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/initconnect"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/keepalive"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotcommon"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotsub"
	"github.com/earlisreal/eTape/engine/internal/feed/opend/pb/qotupdateticker"
)

// Protocol IDs.
const (
	protoInitConnect  = 1001
	protoKeepAlive    = 1004
	protoQotSub       = 3001
	protoUpdateTicker = 3011 // push
)

// Wire frame: 44-byte header + protobuf body.
const headerLen = 44

func encode(protoID, serialNo uint32, body []byte) []byte {
	sum := sha1.Sum(body)
	buf := make([]byte, headerLen+len(body))
	buf[0] = 'F'
	buf[1] = 'T'
	binary.LittleEndian.PutUint32(buf[2:6], protoID)
	buf[6] = 0 // protobuf fmt
	buf[7] = 0
	binary.LittleEndian.PutUint32(buf[8:12], serialNo)
	binary.LittleEndian.PutUint32(buf[12:16], uint32(len(body)))
	copy(buf[16:36], sum[:])
	copy(buf[headerLen:], body)
	return buf
}

type header struct {
	protoID  uint32
	serialNo uint32
	bodyLen  uint32
}

func parseHeader(b []byte) (header, error) {
	if len(b) < headerLen {
		return header{}, fmt.Errorf("short header: %d bytes", len(b))
	}
	if b[0] != 'F' || b[1] != 'T' {
		return header{}, fmt.Errorf("bad magic")
	}
	var h header
	h.protoID = binary.LittleEndian.Uint32(b[2:6])
	h.serialNo = binary.LittleEndian.Uint32(b[8:12])
	h.bodyLen = binary.LittleEndian.Uint32(b[12:16])
	return h, nil
}

func normalizeSymbol(s string) string {
	if !strings.Contains(s, ".") {
		return "US." + s
	}
	return s
}

func marketForPrefix(prefix string) int32 {
	switch prefix {
	case "HK":
		return 1
	case "US":
		return 11
	case "CC":
		return 91
	default:
		return 11 // US default
	}
}

type pendingReq struct {
	ch chan []byte
}

func main() {
	flag.Parse()
	symbol := normalizeSymbol(flag.Arg(0))
	if symbol == "US." {
		symbol = "US.BNRG"
	}
	addr := "127.0.0.1:11111"

	log.Printf("connecting to %s, symbol=%s", addr, symbol)

	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		log.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	r := bufio.NewReaderSize(conn, 128*1024)

	// Serial counter.
	var serial uint32 = 1

	// Pending request map keyed by serialNo.
	pendingReqs := make(map[uint32]pendingReq)

	send := func(protoID, serno uint32, body []byte) error {
		_, err := conn.Write(encode(protoID, serno, body))
		return err
	}

	request := func(protoID uint32, msg proto.Message) ([]byte, error) {
		body, err := proto.Marshal(msg)
		if err != nil {
			return nil, err
		}
		serno := serial
		serial++
		ch := make(chan []byte, 1)
		pendingReqs[serno] = pendingReq{ch: ch}
		defer delete(pendingReqs, serno)

		if err := send(protoID, serno, body); err != nil {
			return nil, err
		}

		select {
		case respBody := <-ch:
			return respBody, nil
		case <-time.After(5 * time.Second):
			return nil, fmt.Errorf("timeout waiting for protoID %d", protoID)
		}
	}

	// Reader goroutine — must start before any request.
	var tickCount int64
	readErr := make(chan error, 1)

	go func() {
		for {
			var head [headerLen]byte
			if _, err := io.ReadFull(r, head[:]); err != nil {
				select {
				case readErr <- err:
				default:
				}
				return
			}
			h, err := parseHeader(head[:])
			if err != nil {
				log.Printf("header parse: %v", err)
				continue
			}

			body := make([]byte, h.bodyLen)
			if _, err := io.ReadFull(r, body); err != nil {
				select {
				case readErr <- err:
				default:
				}
				return
			}

			// Check pending request.
			if p, ok := pendingReqs[h.serialNo]; ok {
				select {
				case p.ch <- append([]byte(nil), body...):
				default:
				}
				continue
			}

			// Push frame.
			switch h.protoID {
			case protoUpdateTicker:
				var resp qotupdateticker.Response
				if err := proto.Unmarshal(body, &resp); err != nil {
					log.Printf("ticker decode: %v", err)
					continue
				}
				if resp.GetRetType() != 0 {
					continue
				}
				s2c := resp.GetS2C()
				for _, t := range s2c.GetTickerList() {
					dir := "N"
					switch qotcommon.TickerDirection(t.GetDir()) {
					case qotcommon.TickerDirection_TickerDirection_Bid:
						dir = "B"
					case qotcommon.TickerDirection_TickerDirection_Ask:
						dir = "S"
					}
					ts := time.Unix(0, int64(math.Round(t.GetTimestamp()*1000))*1e6)
					fmt.Printf("[%s] %s %.4f x%d %s\n",
						ts.Format("15:04:05.000"), symbol, t.GetPrice(), t.GetVolume(), dir)
					atomic.AddInt64(&tickCount, 1)
				}
			case protoKeepAlive:
				// Server keepalive response — ignore.
			}
		}
	}()

	// InitConnect (1001).
	reqIC := &initconnect.Request{C2S: &initconnect.C2S{
		ClientVer:           proto.Int32(100),
		ClientID:            proto.String("tick_sub"),
		RecvNotify:          proto.Bool(true),
		ProgrammingLanguage: proto.String("Go"),
	}}
	respIC, err := request(protoInitConnect, reqIC)
	if err != nil {
		log.Fatalf("initConnect: %v", err)
	}
	var respICMsg initconnect.Response
	if err := proto.Unmarshal(respIC, &respICMsg); err != nil {
		log.Fatalf("initConnect decode: %v", err)
	}
	if respICMsg.GetRetType() != int32(common.RetType_RetType_Succeed) {
		log.Fatalf("initConnect failed: retType=%d msg=%q", respICMsg.GetRetType(), respICMsg.GetRetMsg())
	}
	s2c := respICMsg.GetS2C()
	kaInterval := time.Duration(s2c.GetKeepAliveInterval()) * time.Second
	log.Printf("connected: serverVer=%d connID=%d keepAlive=%v", s2c.GetServerVer(), s2c.GetConnID(), kaInterval)

	// Subscribe TICKER (3001).
	parts := strings.SplitN(symbol, ".", 2)
	market := int32(11)
	if len(parts) == 2 {
		market = marketForPrefix(parts[0])
	}
	code := parts[len(parts)-1]

	reqSub := &qotsub.Request{C2S: &qotsub.C2S{
		SecurityList: []*qotcommon.Security{{
			Market: proto.Int32(market),
			Code:   proto.String(code),
		}},
		SubTypeList:          []int32{4}, // TICKER = 4
		IsSubOrUnSub:         proto.Bool(true),
		IsRegOrUnRegPush:     proto.Bool(true),
		IsFirstPush:          proto.Bool(false),
		ExtendedTime:         proto.Bool(true),
		RegPushRehabTypeList: []int32{0},
	}}
	respSub, err := request(protoQotSub, reqSub)
	if err != nil {
		log.Fatalf("subscribe: %v", err)
	}
	var respSubMsg qotsub.Response
	if err := proto.Unmarshal(respSub, &respSubMsg); err != nil {
		log.Fatalf("subscribe decode: %v", err)
	}
	if respSubMsg.GetRetType() != 0 {
		log.Fatalf("subscribe failed: retType=%d msg=%q", respSubMsg.GetRetType(), respSubMsg.GetRetMsg())
	}
	log.Printf("subscribed TICKER for %s", symbol)

	// Keepalive sender + summary ticker.
	kaTicker := time.NewTicker(kaInterval)
	defer kaTicker.Stop()
	sumTicker := time.NewTicker(time.Second)
	defer sumTicker.Stop()

	fmt.Println("streaming ticks (Ctrl+C to stop)...")
	log.SetOutput(io.Discard) // suppress further log lines, keep tick output clean

	totalTicks := int64(0)
	for {
		select {
		case <-kaTicker.C:
			reqKA := &keepalive.Request{C2S: &keepalive.C2S{
				Time: proto.Int64(time.Now().Unix()),
			}}
			body, _ := proto.Marshal(reqKA)
			serno := serial
			serial++
			if err := send(protoKeepAlive, serno, body); err != nil {
				fmt.Fprintf(os.Stderr, "\nkeepalive: %v\n", err)
				return
			}
		case <-sumTicker.C:
			ticks := atomic.SwapInt64(&tickCount, 0)
			totalTicks += ticks
			// fmt.Fprintf(os.Stderr, "\r[1s: %d ticks | total: %d]   ", ticks, totalTicks)
		case err := <-readErr:
			fmt.Fprintf(os.Stderr, "\nconnection lost: %v\n", err)
			os.Exit(1)
		}
	}
}
