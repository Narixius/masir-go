package xray

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"testing"
	"time"
)

func TestSumTrafficCountsAppInboundsOnly(t *testing.T) {
	got := sumTraffic(map[string]int64{
		"inbound>>>masir-proxy>>>traffic>>>uplink":     100,
		"inbound>>>masir-proxy>>>traffic>>>downlink":   900,
		"inbound>>>tun-in>>>traffic>>>uplink":          20,
		"inbound>>>tun-in>>>traffic>>>downlink":        80,
		"inbound>>>masir-api>>>traffic>>>uplink":       5000,
		"inbound>>>masir-api>>>traffic>>>downlink":     5000,
		"inbound>>>loopback-dns-in>>>traffic>>>uplink": 7,
		"outbound>>>proxy>>>traffic>>>uplink":          12345,
		"inbound>>>odd":                                9,
	})
	if got != (Traffic{Up: 120, Down: 980}) {
		t.Fatalf("got %+v", got)
	}
}

func TestTrafficCountsBytesThroughARunningXray(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			conn, err := echo.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); io.Copy(conn, conn) }()
		}
	}()

	port := mustFreePort(t)
	c := startCore(t, profile(t, socks(port), baseOutbounds, ""))

	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	// SOCKS5: no auth, then CONNECT to the echo server.
	conn.Write([]byte{5, 1, 0})
	if _, err := io.ReadFull(conn, make([]byte, 2)); err != nil {
		t.Fatal(err)
	}
	addr := echo.Addr().(*net.TCPAddr)
	req := append([]byte{5, 1, 0, 1}, addr.IP.To4()...)
	req = append(req, byte(addr.Port>>8), byte(addr.Port))
	conn.Write(req)
	if _, err := io.ReadFull(conn, make([]byte, 10)); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("x"), 4096)
	conn.Write(payload)
	if _, err := io.ReadFull(conn, make([]byte, len(payload))); err != nil {
		t.Fatal(err)
	}

	got, ok, err := c.Traffic(context.Background())
	if err != nil || !ok {
		t.Fatalf("Traffic: ok=%v err=%v", ok, err)
	}
	if got.Up < 4096 || got.Down < 4096 {
		t.Fatalf("counted %+v for 4096 bytes each way", got)
	}

	_ = c.Close()
	if _, ok, _ := c.Traffic(context.Background()); ok {
		t.Fatal("Traffic reports ok after Xray stopped")
	}
}
