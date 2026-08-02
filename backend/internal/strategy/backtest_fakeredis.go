package strategy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"quanty_trade/internal/logger"
)

// backtestFakeRedis is a per-simulation, loopback-only server speaking just
// enough RESP for the strategies' embedded MiniRedis client (AUTH/SELECT/
// SUBSCRIBE/PUBLISH). Evolved strategies stopped using the stdin shim long ago:
// they open real sockets to config redis_addr. Pointing redis_addr at this
// server is what keeps a backtest subprocess off the production redis — three
// backtest clones once connected to the live bus, consumed live candles and
// could have emitted live-strategy_id signals the engine would have executed.
type backtestFakeRedis struct {
	ln     net.Listener
	addr   string
	orders chan map[string]interface{}
	subbed chan string // closed-over first candle-channel subscription

	mu      sync.Mutex
	subConn net.Conn
	subCh   string
	subOnce sync.Once
	done    chan struct{}
}

func newBacktestFakeRedis() (*backtestFakeRedis, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &backtestFakeRedis{
		ln:     ln,
		addr:   ln.Addr().String(),
		orders: make(chan map[string]interface{}, 64),
		subbed: make(chan string, 1),
		done:   make(chan struct{}),
	}
	go s.acceptLoop()
	return s, nil
}

func (s *backtestFakeRedis) Close() {
	close(s.done)
	_ = s.ln.Close()
	s.mu.Lock()
	if s.subConn != nil {
		_ = s.subConn.Close()
	}
	s.mu.Unlock()
}

func (s *backtestFakeRedis) acceptLoop() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.serve(conn)
	}
}

// readCommand parses one client->server RESP array of bulk strings.
func readCommand(r *bufio.Reader) ([]string, error) {
	head, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	head = strings.TrimRight(head, "\r\n")
	if len(head) == 0 || head[0] != '*' {
		return nil, fmt.Errorf("expected RESP array, got %q", head)
	}
	n, err := strconv.Atoi(head[1:])
	if err != nil || n < 0 {
		return nil, fmt.Errorf("bad RESP array header %q", head)
	}
	parts := make([]string, 0, n)
	for i := 0; i < n; i++ {
		sizeLine, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		sizeLine = strings.TrimRight(sizeLine, "\r\n")
		if len(sizeLine) == 0 || sizeLine[0] != '$' {
			return nil, fmt.Errorf("expected bulk string, got %q", sizeLine)
		}
		size, err := strconv.Atoi(sizeLine[1:])
		if err != nil || size < 0 {
			return nil, fmt.Errorf("bad bulk size %q", sizeLine)
		}
		buf := make([]byte, size+2)
		if _, err := ioReadFull(r, buf); err != nil {
			return nil, err
		}
		parts = append(parts, string(buf[:size]))
	}
	return parts, nil
}

func ioReadFull(r *bufio.Reader, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := r.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func (s *backtestFakeRedis) serve(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	for {
		cmd, err := readCommand(r)
		if err != nil {
			return
		}
		if len(cmd) == 0 {
			continue
		}
		switch strings.ToUpper(cmd[0]) {
		case "PING":
			if _, err := conn.Write([]byte("+PONG\r\n")); err != nil {
				return
			}
		case "SUBSCRIBE":
			if len(cmd) < 2 {
				return
			}
			ch := cmd[1]
			s.mu.Lock()
			s.subConn = conn
			s.subCh = ch
			s.mu.Unlock()
			reply := fmt.Sprintf("*3\r\n$9\r\nsubscribe\r\n$%d\r\n%s\r\n:1\r\n", len(ch), ch)
			if _, err := conn.Write([]byte(reply)); err != nil {
				return
			}
			s.subOnce.Do(func() { s.subbed <- ch })
		case "PUBLISH":
			if len(cmd) < 3 {
				return
			}
			ch, payload := cmd[1], cmd[2]
			if strings.Contains(ch, ":signal:") {
				var m map[string]interface{}
				if err := json.Unmarshal([]byte(payload), &m); err == nil {
					select {
					case s.orders <- m:
					default:
						logger.Warnf("[BACKTEST FAKE REDIS] order buffer full, dropped signal on %s", ch)
					}
				}
			}
			if _, err := conn.Write([]byte(":1\r\n")); err != nil {
				return
			}
		default:
			// AUTH / SELECT / anything else the client tries.
			if _, err := conn.Write([]byte("+OK\r\n")); err != nil {
				return
			}
		}
	}
}

// pushCandle delivers one pub/sub "message" frame to the subscribed strategy.
func (s *backtestFakeRedis) pushCandle(payload []byte) error {
	s.mu.Lock()
	conn, ch := s.subConn, s.subCh
	s.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("no subscriber")
	}
	frame := fmt.Sprintf("*3\r\n$7\r\nmessage\r\n$%d\r\n%s\r\n$%d\r\n%s\r\n", len(ch), ch, len(payload), payload)
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, err := conn.Write([]byte(frame))
	return err
}
