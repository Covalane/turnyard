// turnyard-tool-gateway is the single MCP entry point presented to an agent.
// In Docker it runs in a separate tool container and the agent uses proxy mode.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Covalane/turnyard/internal/toolgateway"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("expected local, serve, proxy, or probe")
	}
	mode := args[0]
	flags := flag.NewFlagSet(mode, flag.ContinueOnError)
	configPath := flags.String("config", "", "gateway configuration")
	address := flags.String("address", "", "internal tool gateway address")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if mode == "proxy" || mode == "probe" {
		if *address == "" {
			*address = "turnyard-tools:8081"
		}
		if *configPath != "" {
			return fmt.Errorf("%s does not accept a config", mode)
		}
		if mode == "probe" {
			conn, err := net.DialTimeout("tcp", *address, 2*time.Second)
			if err != nil {
				return err
			}
			return conn.Close()
		}
		return proxy(*address)
	}
	if mode != "local" && mode != "serve" {
		return fmt.Errorf("unknown mode %q", mode)
	}
	if *configPath == "" {
		return fmt.Errorf("missing --config")
	}
	config, err := readConfig(*configPath)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	gateway, err := toolgateway.New(config, logger)
	if err != nil {
		return err
	}
	defer gateway.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := gateway.Verify(ctx); err != nil {
		return err
	}
	if mode == "local" {
		return gateway.Server().Run(ctx, &mcp.StdioTransport{})
	}
	bind := *address
	if bind == "" {
		ip, err := internalIPv4()
		if err != nil {
			return err
		}
		bind = net.JoinHostPort(ip, "8081")
	}
	listener, err := net.Listen("tcp", bind)
	if err != nil {
		return err
	}
	defer listener.Close()
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	logger.InfoContext(ctx, "tool gateway listening", "address", listener.Addr().String())
	var active sync.WaitGroup
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				active.Wait()
				return nil
			}
			return err
		}
		active.Add(1)
		go func() {
			defer active.Done()
			defer conn.Close()
			if err := gateway.Server().Run(ctx, &mcp.IOTransport{Reader: conn, Writer: conn}); err != nil && ctx.Err() == nil {
				logger.WarnContext(ctx, "tool gateway client ended", "error", err)
			}
		}()
	}
}

func readConfig(path string) (toolgateway.Config, error) {
	var config toolgateway.Config
	file, err := os.Open(path)
	if err != nil {
		return config, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return config, err
	}
	return config, nil
}

func proxy(address string) error {
	conn, err := net.DialTimeout("tcp", address, 10*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(conn, os.Stdin)
		if tcp, ok := conn.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
		done <- err
	}()
	_, outputErr := io.Copy(os.Stdout, conn)
	select {
	case inputErr := <-done:
		return errors.Join(inputErr, outputErr)
	default:
		return outputErr
	}
}

func internalIPv4() (string, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}
	for _, item := range interfaces {
		if item.Flags&net.FlagLoopback != 0 || item.Flags&net.FlagUp == 0 {
			continue
		}
		addresses, err := item.Addrs()
		if err != nil {
			return "", err
		}
		for _, address := range addresses {
			ip, _, err := net.ParseCIDR(address.String())
			if err == nil && ip.To4() != nil && !strings.HasPrefix(ip.String(), "169.254.") {
				return ip.String(), nil
			}
		}
	}
	return "", fmt.Errorf("tool gateway has no internal IPv4 interface")
}
