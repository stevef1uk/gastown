package main

import (
	"encoding/json"
	"errors"
	"flag"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/steveyegge/gastown/internal/proxy"
	"github.com/steveyegge/gastown/internal/util"
)

// defaultAllowedSubcmds lists the safe subcommands for gt and bd.
// Dangerous subcommands (e.g. gt polecat, gt rig, gt admin, gt nuke) are excluded.
const defaultAllowedSubcmds = "" +
	"gt:prime,hook,done,mail,nudge,mol,status,handoff,version,convoy,sling;" +
	"bd:create,update,close,show,list,ready,dep,export,prime,stats,blocked,doctor"

// ProxyConfig is the configuration file schema for gt-proxy-server.
// It is loaded from JSON and merged with CLI flags (flags take precedence).
// The default location is ~/gt/.runtime/proxy/config.json.
type ProxyConfig struct {
	// ListenAddr is the address and port to listen on (e.g. "0.0.0.0:9876").
	ListenAddr string `json:"listen_addr"`

	// AdminListenAddr is the address for the local admin HTTP server.
	// Defaults to "127.0.0.1:9877". Set to "" to disable.
	AdminListenAddr string `json:"admin_listen_addr"`

	// CADir is the directory holding ca.crt and ca.key.
	// Defaults to ~/gt/.runtime/ca if empty.
	CADir string `json:"ca_dir"`

	// TownRoot is the Gas Town root directory (e.g. ~/gt).
	// Defaults to $GT_TOWN or ~/gt if empty.
	TownRoot string `json:"town_root"`

	// AllowedCommands is the list of binary names polecats may execute (e.g. ["gt","bd"]).
	AllowedCommands []string `json:"allowed_commands"`

	// AllowedSubcommands maps each allowed command to the subcommands polecats
	// may invoke. Subcommands not listed here are rejected with 403.
	AllowedSubcommands map[string][]string `json:"allowed_subcommands"`

	// ExtraSANIPs lists additional IP addresses to embed as IP Subject Alternative
	// Names in the server TLS certificate.
	//
	// Use this for addresses that containers use to reach the proxy but that are
	// not local interface addresses (and therefore not auto-detected):
	//   - External/NAT IP:  the router's public IP, if you have port forwarding
	//     configured so that containers can reach the proxy from the internet.
	//   - VPN tunnel IP:    the IP assigned to the VPN interface on the remote side.
	//   - Additional LAN IPs: if the host has multiple NICs or aliases.
	//
	// Note: the NAT exit IP (e.g. the IP shown by "curl ifconfig.me") cannot be
	// auto-detected because it is assigned to the router, not to any interface on
	// this machine. It must be listed here explicitly if containers need to connect
	// through NAT.
	ExtraSANIPs []string `json:"extra_san_ips"`

	// ExtraSANHosts lists additional DNS names to embed as DNS Subject Alternative
	// Names in the server TLS certificate.
	//
	// Use this for hostnames that containers resolve to reach the proxy, such as:
	//   - A hostname in the container's /etc/hosts
	//   - A split-horizon DNS entry that resolves to the proxy IP
	//   - A mDNS name (e.g. "macbook.local")
	ExtraSANHosts []string `json:"extra_san_hosts"`
}

// loadConfig reads the config file at path and returns a ProxyConfig.
// If the file does not exist, an empty ProxyConfig is returned (not an error).
// JSON parse errors are returned as errors.
func loadConfig(path string) (ProxyConfig, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is derived from internal config location
	if errors.Is(err, os.ErrNotExist) {
		return ProxyConfig{}, nil
	}
	if err != nil {
		return ProxyConfig{}, err
	}
	var cfg ProxyConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ProxyConfig{}, err
	}
	return cfg, nil
}

func resolveServerConfig(flagSet *flag.FlagSet, configPath string, listen, adminListen, caDir, allowedCmds, allowedSubcmds, townRoot *string) (proxy.Config, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return proxy.Config{}, "", err
	}

	if configPath == "" {
		configPath = filepath.Join(home, "gt", ".runtime", "proxy", "config.json")
	}

	fileCfg, err := loadConfig(configPath)
	if err != nil {
		return proxy.Config{}, "", err
	}

	explicitFlags := make(map[string]bool)
	flagSet.Visit(func(f *flag.Flag) { explicitFlags[f.Name] = true })

	if !explicitFlags["listen"] && fileCfg.ListenAddr != "" {
		*listen = fileCfg.ListenAddr
	}
	if !explicitFlags["admin-listen"] && fileCfg.AdminListenAddr != "" {
		*adminListen = fileCfg.AdminListenAddr
	}
	if !explicitFlags["ca-dir"] && fileCfg.CADir != "" {
		*caDir = fileCfg.CADir
	}
	if !explicitFlags["town-root"] && fileCfg.TownRoot != "" {
		*townRoot = fileCfg.TownRoot
	}
	if !explicitFlags["allowed-cmds"] && len(fileCfg.AllowedCommands) > 0 {
		*allowedCmds = strings.Join(fileCfg.AllowedCommands, ",")
	}
	if !explicitFlags["allowed-subcmds"] && len(fileCfg.AllowedSubcommands) > 0 {
		*allowedSubcmds = buildAllowedSubcmds(fileCfg.AllowedSubcommands)
	}

	if *caDir == "" {
		*caDir = filepath.Join(home, "gt", ".runtime", "ca")
	}
	if *townRoot == "" {
		if v := os.Getenv("GT_TOWN"); v != "" {
			*townRoot = v
		} else {
			*townRoot = filepath.Join(home, "gt")
		}
	}

	var extraSANIPs []net.IP
	for _, s := range fileCfg.ExtraSANIPs {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		ip := net.ParseIP(s)
		if ip == nil {
			slog.Warn("extra_san_ips: invalid IP address — skipping", "entry", s)
			continue
		}
		extraSANIPs = append(extraSANIPs, ip)
	}

	var extraSANHosts []string
	for _, h := range fileCfg.ExtraSANHosts {
		h = strings.TrimSpace(h)
		if h != "" {
			extraSANHosts = append(extraSANHosts, h)
		}
	}

	cmds := strings.Split(*allowedCmds, ",")
	for i := range cmds {
		cmds[i] = strings.TrimSpace(cmds[i])
	}

	cfg := proxy.Config{
		ListenAddr:         *listen,
		AdminListenAddr:    *adminListen,
		AllowedCommands:    cmds,
		AllowedSubcommands: parseAllowedSubcmds(*allowedSubcmds),
		TownRoot:           *townRoot,
		ExtraSANIPs:        extraSANIPs,
		ExtraSANHosts:      extraSANHosts,
	}
	return cfg, *caDir, nil
}

// discoverAllowedSubcmds calls "gt proxy-subcmds" to auto-discover the allowed
// subcommand list. Falls back to defaultAllowedSubcmds if the command is
// unavailable or returns empty output.
func discoverAllowedSubcmds() string {
	cmd := exec.Command("gt", "proxy-subcmds")
	util.SetDetachedProcessGroup(cmd)
	out, err := cmd.Output()
	if err != nil {
		slog.Debug("gt proxy-subcmds discovery failed, using built-in default", "err", err)
		return defaultAllowedSubcmds
	}
	result := strings.TrimSpace(string(out))
	if result == "" {
		return defaultAllowedSubcmds
	}
	return result
}

// buildAllowedSubcmds serializes a map[string][]string back into the semicolon-separated
// "cmd:sub1,sub2,..." format expected by parseAllowedSubcmds.
func buildAllowedSubcmds(m map[string][]string) string {
	if len(m) == 0 {
		return ""
	}
	parts := make([]string, 0, len(m))
	for cmd, subs := range m {
		parts = append(parts, cmd+":"+strings.Join(subs, ","))
	}
	return strings.Join(parts, ";")
}

// parseAllowedSubcmds parses a string of the form
// "gt:prime,hook,done;bd:create,update,close" into a map of command → subcommand set.
func parseAllowedSubcmds(s string) map[string][]string {
	if s == "" {
		return nil
	}
	result := make(map[string][]string)
	for _, part := range strings.Split(s, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		idx := strings.Index(part, ":")
		if idx < 0 {
			continue
		}
		cmd := strings.TrimSpace(part[:idx])
		subsStr := strings.TrimSpace(part[idx+1:])
		var subs []string
		for _, sub := range strings.Split(subsStr, ",") {
			sub = strings.TrimSpace(sub)
			if sub != "" {
				subs = append(subs, sub)
			}
		}
		if cmd != "" && len(subs) > 0 {
			result[cmd] = subs
		}
	}
	return result
}
