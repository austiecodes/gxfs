package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/zeromicro/go-zero/rest"

	"gxfs/internal/config"
	"gxfs/internal/server"
	"gxfs/internal/store/memory"
	"gxfs/internal/vfs"
)

func splitAddr(addr string) (string, int, error) {
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		if strings.HasPrefix(addr, ":") {
			host = "0.0.0.0"
			portText = strings.TrimPrefix(addr, ":")
		} else {
			return "", 0, fmt.Errorf("invalid addr %q: %w", addr, err)
		}
	}
	if host == "" {
		host = "0.0.0.0"
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return "", 0, fmt.Errorf("invalid port %q: %w", portText, err)
	}
	return host, port, nil
}

func main() {
	path := os.Getenv("GXFS_SERVER_CONFIG")
	if path == "" {
		path = "gxfs-server.toml"
	}
	cfg, err := config.LoadServer(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	host, port, err := splitAddr(cfg.Addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	tree, err := vfs.New(nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	handler := server.NewHandler(memory.New(tree))

	srv := rest.MustNewServer(rest.RestConf{Host: host, Port: port})
	defer srv.Stop()

	srv.AddRoute(rest.Route{Method: http.MethodGet, Path: "/healthz", Handler: handler.ServeHTTP})
	srv.AddRoute(rest.Route{Method: http.MethodGet, Path: "/v1/repos/:repo/:op", Handler: handler.ServeHTTP})
	srv.Start()
}
