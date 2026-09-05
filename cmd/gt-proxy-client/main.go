// gt-proxy-client is the pass-through binary installed in containers as both `gt` and `bd`.
// When GT_PROXY_URL, GT_PROXY_CERT, GT_PROXY_KEY, and GT_PROXY_CA are all set, it forwards
// os.Args[1:] to the proxy server over mTLS and proxies the response.
// Otherwise it execs the real binary at /usr/local/bin/gt.real (or the path in GT_REAL_BIN).
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)

type execRequest struct {
	Argv []string `json:"argv"`
}

type execResponse struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exitCode"`
}

func main() {
	cfg := loadClientConfig()
	if !cfg.isEnabled() {
		execReal(cfg.RealBin)
		return
	}

	httpClient, err := cfg.httpClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "gt-proxy-client: load client cert/CA: %v\n", err)
		os.Exit(1)
	}

	argv := append([]string{toolNameFromArg0(os.Args[0])}, os.Args[1:]...)
	body, err := json.Marshal(execRequest{Argv: argv})
	if err != nil {
		fmt.Fprintf(os.Stderr, "gt-proxy-client: encode request: %v\n", err)
		os.Exit(1)
	}

	resp, err := httpClient.Post(cfg.ProxyURL+"/v1/exec", "application/json", bytes.NewReader(body)) //nolint:gosec // proxy URL comes from trusted env var
	if err != nil {
		fmt.Fprintf(os.Stderr, "gt-proxy-client: proxy request failed: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close() //nolint:errcheck // best-effort close on response body

	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "gt-proxy-client: server error %d: %s\n", resp.StatusCode, msg)
		os.Exit(1)
	}

	var result execResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		fmt.Fprintf(os.Stderr, "gt-proxy-client: decode response: %v\n", err)
		os.Exit(1)
	}

	if result.Stdout != "" {
		_, _ = fmt.Fprint(os.Stdout, result.Stdout)
	}
	if result.Stderr != "" {
		_, _ = fmt.Fprint(os.Stderr, result.Stderr)
	}
	os.Exit(result.ExitCode)
}
