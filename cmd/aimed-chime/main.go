// aimed-chime: an MCP server and CLI that plays a chime or speaks text on a chosen audio output device.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/aki-kuramoto/aimed-chime/internal/server"
	"github.com/aki-kuramoto/aimed-chime/internal/sound"
)

const usage = `usage: aimed-chime <command> [flags]

commands:
  serve                              run the MCP server over stdio
  chime  [--device D] [--count N]    play the chime N times (default 1)
  speak  [--device D] [--voice V] TEXT...
                                     speak the text (--voice overrides the config's voice)
  devices                            list the audio devices with the remarks from the config file
  config-path                        print the config file path

  --device is part of the device's name or UID (case-insensitive, must match one output device).

config: ~/.app-data/aimed-chime/config.setay (optional; read again on every call)
  devices = [ { match; remarks; default; chime-volume; speech-volume }, ... ]
  chime-volume (default 6), speech-volume (default 1.4), voice (default: system voice), ffmpeg
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	path, err := sound.ConfigPath()
	if err != nil {
		die(err)
	}
	p := &sound.Player{ConfigPath: path}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	args := os.Args[2:]
	switch os.Args[1] {
	case "serve":
		err = serve(ctx, p)
	case "chime":
		err = chime(ctx, p, args)
	case "speak":
		err = speak(ctx, p, args)
	case "devices":
		var l sound.Listing
		if l, err = p.List(ctx); err == nil {
			fmt.Print(server.ListText(l))
		}
	case "config-path":
		fmt.Println(path)
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		die(err)
	}
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "aimed-chime:", err)
	os.Exit(1)
}

func serve(ctx context.Context, p *sound.Player) error {
	err := server.New(p).Run(ctx, &mcp.StdioTransport{})
	// The client closing stdin, or a signal, is a normal shutdown.
	// The SDK returns errors from its internal jsonrpc2 package, so match on the text as well.
	if err != nil && (errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) ||
		strings.Contains(err.Error(), "EOF") || strings.Contains(err.Error(), "is closing")) {
		return nil
	}
	return err
}

func chime(ctx context.Context, p *sound.Player, args []string) error {
	fs := flag.NewFlagSet("chime", flag.ContinueOnError)
	device := fs.String("device", "", "part of the device name or UID")
	count := fs.Int("count", 1, "how many times to play the chime")
	if err := fs.Parse(args); err != nil {
		return err
	}
	d, err := p.Chime(ctx, *device, *count)
	if err != nil {
		return err
	}
	fmt.Printf("played %d time(s) on %s\n", *count, d)
	return nil
}

func speak(ctx context.Context, p *sound.Player, args []string) error {
	fs := flag.NewFlagSet("speak", flag.ContinueOnError)
	device := fs.String("device", "", "part of the device name or UID")
	voice := fs.String("voice", "", "text-to-speech voice (default: the config's voice, else the system default)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	d, used, err := p.Speak(ctx, *device, strings.Join(fs.Args(), " "), *voice)
	if err != nil {
		return err
	}
	fmt.Printf("spoke on %s with voice %s\n", d, used)
	return nil
}
