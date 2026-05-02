package main

import "testing"

func TestSplitAddr(t *testing.T) {
	tests := []struct {
		name string
		addr string
		host string
		port int
	}{
		{name: "port only", addr: ":7635", host: "0.0.0.0", port: 7635},
		{name: "host and port", addr: "127.0.0.1:9000", host: "127.0.0.1", port: 9000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host, port, err := splitAddr(tt.addr)
			if err != nil {
				t.Fatalf("splitAddr() error = %v", err)
			}
			if host != tt.host || port != tt.port {
				t.Fatalf("splitAddr() = %s %d, want %s %d", host, port, tt.host, tt.port)
			}
		})
	}
}
