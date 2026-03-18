package main

import (
	"context"
	"fmt"
	"os"

	"qrcheck/internal/app"
	"qrcheck/internal/cli"
)

var version = "dev"

func main() {
	opts, err := cli.Parse(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	code, err := app.Execute(context.Background(), opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	os.Exit(code)
}
