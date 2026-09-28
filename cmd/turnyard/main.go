package main

import (
	"context"
	"os"

	"github.com/Covalane/turnyard/internal/transport"
)

func main() {
	os.Exit(transport.CLI(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}
