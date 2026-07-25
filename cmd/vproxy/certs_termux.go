package main

import (
	"os"
	"runtime"
)

func setupTermuxCerts() {
	// Termux (Android) 的 CA 证书路径与标准 Linux 不同。
	// Go 静态编译的二进制默认查 /etc/ssl/certs/，但 Termux 的证书在 $PREFIX/etc/tls/。
	// 如果用户未手动设置 SSL_CERT_FILE，自动探测并设置。
	if (runtime.GOOS == "linux" || runtime.GOOS == "android") && os.Getenv("TERMUX_VERSION") != "" {
		if os.Getenv("SSL_CERT_FILE") == "" {
			prefix := os.Getenv("PREFIX")
			if prefix == "" {
				prefix = "/data/data/com.termux/files/usr"
			}
			candidates := []string{
				prefix + "/etc/tls/cert.pem",
				prefix + "/etc/ssl/certs/ca-certificates.crt",
				prefix + "/etc/pki/tls/certs/ca-bundle.crt",
			}
			for _, c := range candidates {
				if _, err := os.Stat(c); err == nil {
					_ = os.Setenv("SSL_CERT_FILE", c)
					break
				}
			}
		}
	}
}
