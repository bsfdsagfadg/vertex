package transport

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

// socks5DialFunc 返回一个直连指定 SOCKS5 地址的拨号函数。
// 用于自引用场景：第二跳 URI 与前置代理池内实例一致时，直接经回环 SOCKS 拨号。
func socks5DialFunc(socksAddr string) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		dialCtx, cancel := context.WithTimeout(ctx, secondHopDialTimeout)
		defer cancel()
		conn, err := (&net.Dialer{}).DialContext(dialCtx, "tcp", socksAddr)
		if err != nil {
			return nil, fmt.Errorf("socks5 dial to %s: %w", socksAddr, err)
		}
		if deadline, ok := dialCtx.Deadline(); ok {
			conn.SetDeadline(deadline)
		}

		// SOCKS5 方法协商：无认证
		if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
			conn.Close()
			return nil, fmt.Errorf("socks5 handshake write: %w", err)
		}
		buf := make([]byte, 2)
		if _, err := io.ReadFull(conn, buf); err != nil {
			conn.Close()
			return nil, fmt.Errorf("socks5 handshake read: %w", err)
		}
		if buf[0] != 5 || buf[1] != 0 {
			conn.Close()
			return nil, fmt.Errorf("socks5 handshake rejected: ver=%d method=%d", buf[0], buf[1])
		}

		// CONNECT 请求
		host, portStr, err := net.SplitHostPort(addr)
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("socks5 target parse: %w", err)
		}
		portNum, _ := strconv.Atoi(portStr)

		var atyp byte
		var addrBytes []byte
		if ip := net.ParseIP(host); ip != nil {
			if ip4 := ip.To4(); ip4 != nil {
				atyp = 1
				addrBytes = ip4
			} else {
				atyp = 4
				addrBytes = ip.To16()
			}
		} else {
			atyp = 3
			addrBytes = []byte(host)
		}

		req := []byte{5, 1, 0, atyp}
		if atyp == 3 {
			req = append(req, byte(len(addrBytes)))
		}
		req = append(req, addrBytes...)
		req = append(req, byte(portNum>>8), byte(portNum))

		if _, err := conn.Write(req); err != nil {
			conn.Close()
			return nil, fmt.Errorf("socks5 connect write: %w", err)
		}

		resp := make([]byte, 4)
		if _, err := io.ReadFull(conn, resp); err != nil {
			conn.Close()
			return nil, fmt.Errorf("socks5 connect read: %w", err)
		}
		if resp[1] != 0 {
			conn.Close()
			return nil, fmt.Errorf("socks5 connect failed: code=%d", resp[1])
		}

		// 读剩余的 BND.ADDR（最多 256 字节）以完成握手
		restLen := 0
		switch resp[3] {
		case 1:
			restLen = 4
		case 3:
			domainLenBuf := make([]byte, 1)
			if _, err := io.ReadFull(conn, domainLenBuf); err != nil {
				conn.Close()
				return nil, fmt.Errorf("socks5 response domain length read: %w", err)
			}
			n := int(domainLenBuf[0])
			restLen = n
			if restLen > 256 {
				conn.Close()
				return nil, fmt.Errorf("socks5 response domain too long")
			}
		case 4:
			restLen = 16
		}
		if restLen > 0 {
			rest := make([]byte, restLen+2)
			if _, err := io.ReadFull(conn, rest); err != nil {
				conn.Close()
				return nil, fmt.Errorf("socks5 response read: %w", err)
			}
		}

		_ = conn.SetDeadline(time.Time{})
		return conn, nil
	}
}
