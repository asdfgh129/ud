// udctl is the CLI control-plane client (docs/07 §3). P0 implements the L0
// subset; release-stage commands explicitly report that they are out of scope.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/asdfgh129/ud/internal/app"
	"github.com/asdfgh129/ud/internal/version"
)

var (
	addrFlag   string
	tokenFlag  string
	configPath string
	rawJSON    bool
)

type cli struct {
	base  string
	token string
	http  *http.Client
}

func main() {
	fs := flag.NewFlagSet("udctl", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.StringVar(&addrFlag, "addr", "", "daemon address (default: UD_ADDR or local discovery file)")
	fs.StringVar(&tokenFlag, "token", "", "bearer token (default: UD_TOKEN)")
	fs.StringVar(&configPath, "config", "", "config.yaml (only for `udctl daemon`)")
	fs.BoolVar(&rawJSON, "json", false, "print raw JSON without indentation")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	args := fs.Args()
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	if args[0] == "version" || args[0] == "--version" {
		fmt.Println("udctl", version.String())
		return
	}
	if args[0] == "daemon" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := app.Run(ctx, app.Options{ConfigPath: configPath, Listen: addrFlag}); err != nil {
			fatal(err)
		}
		return
	}

	c := newCLI()
	var err error
	switch args[0] {
	case "add":
		err = c.add(args[1:])
	case "list":
		err = c.list(args[1:])
	case "show":
		err = c.show(args[1:])
	case "pause", "resume", "cancel":
		err = c.action(args[0], args[1:])
	case "rm":
		err = c.remove(args[1:])
	case "resolve":
		err = c.resolve(args[1:])
	case "capabilities", "stats", "health":
		err = c.simple("GET", "/api/v1/"+map[string]string{"capabilities": "capabilities", "stats": "stats", "health": "healthz"}[args[0]], nil)
	case "config":
		err = c.config(args[1:])
	case "backup", "restore", "migrate", "plugin":
		fmt.Fprintf(os.Stderr, "udctl %s: 尚未实现（P0 范围外，见 docs/14 WBS 19）\n", args[0])
		os.Exit(2)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fatal(err)
	}
}

func newCLI() *cli {
	base := addrFlag
	if base == "" {
		base = os.Getenv("UD_ADDR")
	}
	if base == "" {
		if d, err := app.ReadDiscovery(); err == nil && d.Addr != "" {
			base = d.Addr
		}
	}
	if base == "" {
		fmt.Fprintln(os.Stderr, "找不到 daemon：请用 --addr 指定、设置 UD_ADDR，或先启动 udd（本地发现文件）")
		os.Exit(2)
	}
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		base = "http://" + base
	}
	token := tokenFlag
	if token == "" {
		token = os.Getenv("UD_TOKEN")
	}
	return &cli{base: strings.TrimSuffix(base, "/"), token: token,
		http: &http.Client{Timeout: 30 * time.Second}}
}

func (c *cli) do(method, path string, body any) ([]byte, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, r)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("daemon 连接失败: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		var envelope struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &envelope) == nil && envelope.Error.Code != "" {
			return nil, fmt.Errorf("%s: %s", envelope.Error.Code, envelope.Error.Message)
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return data, nil
}

func output(data []byte) error {
	if rawJSON {
		_, err := os.Stdout.Write(data)
		return err
	}
	var v any
	if json.Unmarshal(data, &v) != nil {
		_, err := os.Stdout.Write(data)
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(b))
	return nil
}

func (c *cli) add(args []string) error {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	var dir, limit, out, post string
	var conn int
	fs.StringVar(&dir, "dir", "", "output directory")
	fs.StringVar(&limit, "limit", "", "per-task rate, e.g. 2MiB/s")
	fs.StringVar(&out, "out", "", "output filename")
	fs.StringVar(&post, "post-process", "none", "postprocessor name")
	fs.IntVar(&conn, "conn", 0, "max connections")
	if err := fs.Parse(reorderFlags(args)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("用法: udctl add <input> [--dir] [--limit] [--conn] [--out] [--post-process]")
	}
	body := map[string]any{
		"input": fs.Arg(0),
		"options": map[string]any{
			"dir": dir, "maxDownloadLimit": limit, "maxConnections": conn,
			"out": out, "postProcess": post,
		},
	}
	data, err := c.do("POST", "/api/v1/tasks", body)
	if err != nil {
		return err
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(data, &created)
	if created.ID != "" && !rawJSON {
		fmt.Println("任务已创建:", created.ID)
	}
	return output(data)
}

func (c *cli) list(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	var state string
	fs.StringVar(&state, "state", "", "filter by state")
	if err := fs.Parse(reorderFlags(args)); err != nil {
		return err
	}
	data, err := c.do("GET", "/api/v1/tasks?state="+state, nil)
	if err != nil {
		return err
	}
	return output(data)
}

func (c *cli) show(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("用法: udctl show <id>")
	}
	data, err := c.do("GET", "/api/v1/tasks/"+args[0], nil)
	if err != nil {
		return err
	}
	return output(data)
}

func (c *cli) action(name string, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("用法: udctl %s <id>", name)
	}
	data, err := c.do("POST", "/api/v1/tasks/"+args[0]+"/"+name, nil)
	if err != nil {
		return err
	}
	return output(data)
}

func (c *cli) remove(args []string) error {
	fs := flag.NewFlagSet("rm", flag.ContinueOnError)
	withFiles := fs.Bool("with-files", false, "also delete downloaded files")
	if err := fs.Parse(reorderFlags(args)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("用法: udctl rm <id> [--with-files]")
	}
	path := "/api/v1/tasks/" + fs.Arg(0)
	if *withFiles {
		path += "?withFiles=true"
	}
	data, err := c.do("DELETE", path, nil)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		fmt.Println("已删除")
		return nil
	}
	return output(data)
}

func (c *cli) resolve(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("用法: udctl resolve <input>")
	}
	data, err := c.do("POST", "/api/v1/resolve", map[string]any{"input": args[0]})
	if err != nil {
		return err
	}
	return output(data)
}

func (c *cli) simple(method, path string, body any) error {
	data, err := c.do(method, path, body)
	if err != nil {
		return err
	}
	return output(data)
}

func (c *cli) config(args []string) error {
	if len(args) == 0 || args[0] == "get" {
		data, err := c.do("GET", "/api/v1/config", nil)
		if err != nil {
			return err
		}
		if len(args) == 2 {
			var m map[string]any
			_ = json.Unmarshal(data, &m)
			return output(mustJSON(m[args[1]]))
		}
		return output(data)
	}
	if args[0] == "set" && len(args) == 3 {
		data, err := c.do("PATCH", "/api/v1/config", map[string]any{args[1]: args[2]})
		if err != nil {
			return err
		}
		return output(data)
	}
	return fmt.Errorf("用法: udctl config get [key] | udctl config set <key> <value>")
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// reorderFlags lets users put options after the positional argument
// (`udctl add <url> --conn 4`), which Go's flag package does not support.
func reorderFlags(args []string) []string {
	boolFlags := map[string]bool{
		"--with-files": true, "-with-files": true,
	}
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			if !strings.Contains(a, "=") && !boolFlags[a] && i+1 < len(args) &&
				!strings.HasPrefix(args[i+1], "-") {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		positional = append(positional, a)
	}
	return append(flags, positional...)
}

func usage() {
	fmt.Fprintln(os.Stderr, `udctl — Universal Downloader CLI (P0 / L0)

用法:
  udctl [--addr host:port] [--token T] <command>

  add <input> [--dir] [--limit] [--conn] [--out] [--post-process]
  list [--state]            show <id> | pause <id> | resume <id> | cancel <id>
  rm <id> [--with-files]    resolve <input>
  capabilities | stats | health
  config get [key] | config set <flat.key> <value>
  daemon [--config <file>] [--listen 127.0.0.1:0]
  version

P0 未实现: backup / restore / migrate / plugin (见 docs/14 WBS 19)。
daemon 地址优先级: --addr > UD_ADDR > 本地发现文件（不占用固定 6800 端口）。`)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "udctl:", err)
	os.Exit(1)
}
