// local-install builds aimed-chime from this working tree and installs it, for working on aimed-chime itself.
// Users install it with `go install github.com/aki-kuramoto/aimed-chime/cmd/aimed-chime@latest` instead.
//
//	go run ./tools/local-install            vet, test, build, install
//	go run ./tools/local-install -q         skip vet and test
//	go run ./tools/local-install -dir DIR   install into DIR instead of where `go install` would put it
//
// The binary is built next to the destination under a temporary name and then moved into place,
// so MCP servers that are running from the old binary keep working (see replace_*.go).
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const name = "aimed-chime"

func main() {
	quick := flag.Bool("q", false, "skip go vet and go test")
	dir := flag.String("dir", "", "install directory (default: $(go env GOBIN), else $(go env GOPATH)/bin)")
	flag.Parse()
	if err := run(*quick, *dir); err != nil {
		fmt.Fprintln(os.Stderr, "local-install:", err)
		os.Exit(1)
	}
}

func run(quick bool, dir string) error {
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	if !quick {
		if err := goCmd(root, "vet", "./..."); err != nil {
			return err
		}
		if err := goCmd(root, "test", "./..."); err != nil {
			return err
		}
	}
	if dir == "" {
		if dir, err = installDir(); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	exe := ""
	if runtime.GOOS == "windows" {
		exe = ".exe"
	}
	dest := filepath.Join(dir, name+exe)
	tmp, err := os.CreateTemp(dir, "."+name+"-*"+exe)
	if err != nil {
		return err
	}
	tmp.Close()
	if err := goCmd(root, "build", "-o", tmp.Name(), "./cmd/"+name); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := replace(tmp.Name(), dest); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	fmt.Println("==> installed", dest)
	fmt.Println("Running MCP sessions keep the old binary; newly started sessions use the new one.")
	fmt.Printf("Register it once with your MCP client, e.g.: claude mcp add --scope user %s -- %q serve\n", name, dest)
	return nil
}

func goCmd(dir string, args ...string) error {
	fmt.Println("==> go", strings.Join(args, " "))
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

func goEnv(key string) (string, error) {
	out, err := exec.Command("go", "env", key).Output()
	if err != nil {
		return "", fmt.Errorf("go env %s: %w", key, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func moduleRoot() (string, error) {
	mod, err := goEnv("GOMOD")
	if err != nil {
		return "", err
	}
	if mod == "" || mod == os.DevNull {
		return "", fmt.Errorf("run it inside the %s repository", name)
	}
	return filepath.Dir(mod), nil
}

// installDir is where `go install` would put the binary.
func installDir() (string, error) {
	if bin, err := goEnv("GOBIN"); err != nil || bin != "" {
		return bin, err
	}
	gopath, err := goEnv("GOPATH")
	if err != nil {
		return "", err
	}
	// GOPATH can be a list; go install uses the first entry.
	return filepath.Join(filepath.SplitList(gopath)[0], "bin"), nil
}
