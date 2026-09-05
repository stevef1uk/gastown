package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

type clientConfig struct {
	ProxyURL string
	CertFile string
	KeyFile  string
	CAFile   string
	RealBin  string
}

func loadClientConfig() clientConfig {
	cfg := clientConfig{
		ProxyURL: os.Getenv("GT_PROXY_URL"),
		CertFile: os.Getenv("GT_PROXY_CERT"),
		KeyFile:  os.Getenv("GT_PROXY_KEY"),
		CAFile:   os.Getenv("GT_PROXY_CA"),
		RealBin:  os.Getenv("GT_REAL_BIN"),
	}
	if cfg.RealBin == "" {
		cfg.RealBin = "/usr/local/bin/gt.real"
	}
	return cfg
}

func (c clientConfig) isEnabled() bool {
	return c.ProxyURL != "" && c.CertFile != "" && c.KeyFile != "" && c.CAFile != ""
}

func (c clientConfig) httpClient() (*http.Client, error) {
	clientCert, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
	if err != nil {
		return nil, err
	}

	caPEM, err := os.ReadFile(c.CAFile) //nolint:gosec // CA path from trusted env var
	if err != nil {
		return nil, err
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("invalid CA PEM")
	}

	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{clientCert},
		MinVersion:   tls.VersionTLS12,
		RootCAs:      pool,
	}

	return &http.Client{
		Timeout:   5 * time.Minute,
		Transport: &http.Transport{TLSClientConfig: tlsCfg},
	}, nil
}

func toolNameFromArg0(arg0 string) string {
	return filepath.Base(arg0)
}

func execReal(realBin string) {
	if realBin == "" {
		realBin = "/usr/local/bin/gt.real"
	}
	if err := syscall.Exec(realBin, os.Args, os.Environ()); err != nil { //nolint:gosec // realBin is from env or default
		fmt.Fprintf(os.Stderr, "gt-proxy-client: exec %s: %v\n", realBin, err)
		os.Exit(1)
	}
}
