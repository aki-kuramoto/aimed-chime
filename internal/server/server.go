// Package server is the MCP server: three tools, chime, speak and list_devices.
package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/aki-kuramoto/aimed-chime/internal/sound"
)

const Version = "0.1.0"

// Instructions are the server-wide instructions that clients show to the model.
// The instructions in the config file are added to them when the server starts.
const Instructions = `aimed-chime: play a chime or speak text on a chosen audio output device.

The device is chosen by name, per call. list_devices shows each output device with the remarks
the user wrote about it in the config file; use them to choose the device, unless the user told
you which one to use. If device is omitted, the device marked default in the config file is
used. If the device is missing the call fails; it never falls back to the system default
output.`

// New creates the MCP server.
func New(p *sound.Player) *mcp.Server {
	// Clients read the server instructions only when a session starts, so the config file's
	// instructions are also added to every tool result, which picks up edits without a restart.
	instructions := Instructions
	if cfg, err := sound.LoadConfig(p.ConfigPath); err == nil {
		instructions += userInstructions(cfg.Instructions)
	}
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "aimed-chime",
		Title:   "Chime or speak on a chosen audio device",
		Version: Version,
	}, &mcp.ServerOptions{Instructions: instructions})
	h := &handlers{p: p}
	h.register(srv)
	return srv
}

type handlers struct {
	p *sound.Player
}

// userInstructions formats the instructions from the config file, or returns "" when there are none.
func userInstructions(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nInstructions from the user (written in the config file; follow them when using these tools):")
	for _, l := range lines {
		b.WriteString("\n- " + l)
	}
	return b.String()
}

// result adds the config file's current instructions to a successful tool result.
func (h *handlers) result(s string) *mcp.CallToolResult {
	if cfg, err := sound.LoadConfig(h.p.ConfigPath); err == nil {
		s += userInstructions(cfg.Instructions)
	}
	return textResult(s)
}

const deviceDesc = "part of the output device's name or UID as shown by list_devices (case-insensitive; must match exactly one output device). " +
	"The remarks list_devices shows can help to choose it. Omit to use the device marked default in the config file"

// --- chime --------------------------------------------------------------

type chimeIn struct {
	Count  int    `json:"count,omitempty" jsonschema:"how many times to play the chime (default 1)"`
	Device string `json:"device,omitempty"`
}

func (h *handlers) chime(ctx context.Context, req *mcp.CallToolRequest, in chimeIn) (*mcp.CallToolResult, any, error) {
	if in.Count == 0 {
		in.Count = 1
	}
	d, err := h.p.Chime(ctx, in.Device, in.Count)
	if err != nil {
		return errResult(err), nil, nil
	}
	return h.result(fmt.Sprintf("played the chime %d time(s) on %s", in.Count, d)), nil, nil
}

// --- speak --------------------------------------------------------------

type speakIn struct {
	Text   string `json:"text" jsonschema:"text to speak, e.g. 'The work is done. Please take a look.'"`
	Device string `json:"device,omitempty"`
}

func (h *handlers) speak(ctx context.Context, req *mcp.CallToolRequest, in speakIn) (*mcp.CallToolResult, any, error) {
	d, voice, err := h.p.Speak(ctx, in.Device, in.Text, "")
	if err != nil {
		return errResult(err), nil, nil
	}
	return h.result(fmt.Sprintf("spoke on %s with voice %s", d, voice)), nil, nil
}

// --- list_devices -------------------------------------------------------

func (h *handlers) listDevices(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, any, error) {
	l, err := h.p.List(ctx)
	if err != nil {
		return errResult(err), nil, nil
	}
	return textResult(ListText(l)), nil, nil
}

// ListText formats the listing for both people and models. The devices CLI command uses it too.
func ListText(l sound.Listing) string {
	var b strings.Builder
	b.WriteString("Output devices (pass the name as device):\n")
	for _, d := range l.Outputs {
		fmt.Fprintf(&b, "- %s\n", d.Device)
		if d.Entry == nil {
			b.WriteString("    remarks: (not in the config file)\n")
		} else {
			if d.Entry.Default {
				b.WriteString("    default: used when device is omitted\n")
			}
			if d.Entry.Remarks != "" {
				fmt.Fprintf(&b, "    remarks: %s\n", d.Entry.Remarks)
			}
		}
		fmt.Fprintf(&b, "    volume: chime %g, speech %g\n", d.ChimeVolume, d.SpeechVolume)
	}
	if len(l.Disconnected) > 0 {
		b.WriteString("In the config file but not connected now (or not matching exactly one output device):\n")
		for _, e := range l.Disconnected {
			fmt.Fprintf(&b, "- match %q", e.Match)
			if e.Remarks != "" {
				fmt.Fprintf(&b, ": %s", e.Remarks)
			}
			b.WriteString("\n")
		}
	}
	if len(l.Inputs) > 0 {
		b.WriteString("Input-only devices (cannot play):\n")
		for _, d := range l.Inputs {
			fmt.Fprintf(&b, "- %s\n", d)
		}
	}
	fmt.Fprintf(&b, "Config file: %s", l.ConfigPath)
	b.WriteString(userInstructions(l.Config.Instructions))
	return b.String() + "\n"
}

// --- registration -------------------------------------------------------

func (h *handlers) register(srv *mcp.Server) {
	f, t := false, true

	chimeSchema := must(jsonschema.For[chimeIn](nil))
	chimeSchema.Properties["count"].Minimum = ptr(1.0)
	chimeSchema.Properties["device"].Description = deviceDesc
	mcp.AddTool(srv, &mcp.Tool{
		Name:  "chime",
		Title: "Play a chime on a chosen device",
		Description: "Play a short three-tone chime on the chosen audio output device. " +
			"Returns after the sound has finished.",
		InputSchema: chimeSchema,
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &f, OpenWorldHint: &t},
	}, h.chime)

	speakSchema := must(jsonschema.For[speakIn](nil))
	speakSchema.Properties["device"].Description = deviceDesc
	mcp.AddTool(srv, &mcp.Tool{
		Name:  "speak",
		Title: "Speak text on a chosen device",
		Description: "Speak text with the system's text-to-speech on the chosen audio output device. " +
			"Returns after the speech has finished.",
		InputSchema: speakSchema,
		Annotations: &mcp.ToolAnnotations{DestructiveHint: &f, OpenWorldHint: &t},
	}, h.speak)

	mcp.AddTool(srv, &mcp.Tool{
		Name:  "list_devices",
		Title: "List audio devices",
		Description: "List the audio output devices with the remarks the user wrote about them in the config file, " +
			"their volumes and which one is the default; also config entries that are not connected and input-only devices. " +
			"Call it before chime / speak to choose the device; pass the name or a distinctive part of the UID as device.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: &f, IdempotentHint: true},
	}, h.listDevices)
}

func textResult(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

func errResult(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func ptr[T any](v T) *T { return &v }
