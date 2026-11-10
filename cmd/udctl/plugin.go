package main

import (
	"bufio"
	"crypto/ed25519"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/asdfgh129/ud/internal/plugin"
)

// pluginCommand implements the offline rule-pack workflow of docs/08 §8.
// None of these subcommands need a running daemon or a network connection.
func pluginCommand(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("用法: udctl plugin <scaffold|keygen|sign|verify|test|list> ...\n" +
			"运行 `udctl` 查看完整用法")
	}
	switch args[0] {
	case "scaffold":
		return pluginScaffold(args[1:])
	case "keygen":
		return pluginKeygen(args[1:])
	case "sign":
		return pluginSign(args[1:])
	case "verify":
		return pluginVerify(args[1:])
	case "test":
		return pluginTest(args[1:])
	case "list":
		return pluginList(args[1:])
	default:
		return fmt.Errorf("未知的 plugin 子命令 %q", args[0])
	}
}

func pluginScaffold(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("用法: udctl plugin scaffold <dir>")
	}
	dir := args[0]
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for name, content := range map[string]string{
		"manifest.json": scaffoldManifest,
		"index.js":      scaffoldScript,
		"cases.json":    scaffoldFixture,
	} {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return fmt.Errorf("%s 已存在，拒绝覆盖", p)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			return err
		}
	}
	fmt.Println("已生成插件骨架:", dir)
	fmt.Println("下一步: udctl plugin test", dir, "--fixture", filepath.Join(dir, "cases.json"), "--insecure-plugins")
	return nil
}

func pluginKeygen(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("用法: udctl plugin keygen（不接受参数）")
	}
	pub, priv, err := plugin.GenerateKeyPair()
	if err != nil {
		return err
	}
	fmt.Println("public: ", pub)
	fmt.Println("private:", priv)
	fmt.Fprintln(os.Stderr, "警告: 私钥仅用于离线签名，请存为 0600 权限文件，切勿提交到仓库或放进插件目录。")
	return nil
}

func pluginSign(args []string) error {
	fs := flag.NewFlagSet("plugin sign", flag.ContinueOnError)
	keyPath := fs.String("key", "", "file containing a base64 Ed25519 private key")
	if err := fs.Parse(reorderFlags(args)); err != nil {
		return err
	}
	if fs.NArg() != 1 || *keyPath == "" {
		return fmt.Errorf("用法: udctl plugin sign <dir> --key <file>")
	}
	priv, err := readPrivateKey(*keyPath)
	if err != nil {
		return err
	}
	m, err := plugin.SignPack(fs.Arg(0), priv)
	if err != nil {
		return err
	}
	fmt.Printf("已签名 %s v%s (%s)\n", m.Name, m.Version, m.Entry)
	return nil
}

func pluginVerify(args []string) error {
	fs := flag.NewFlagSet("plugin verify", flag.ContinueOnError)
	keyPath := fs.String("key", "", "file containing one or more base64 Ed25519 public keys")
	insecure := fs.Bool("insecure-plugins", insecurePlugins, "skip signature verification (local dev)")
	if err := fs.Parse(reorderFlags(args)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("用法: udctl plugin verify <dir> [--key <file>] [--insecure-plugins]")
	}
	keys, err := loadKeys(*keyPath, *insecure)
	if err != nil {
		return err
	}
	m, err := plugin.VerifyPack(fs.Arg(0), keys, true, *insecure)
	if err != nil {
		return err
	}
	fmt.Printf("OK: %s v%s (apiVersion=%s runtime=%s match=%d rules, limits=%dms/%dMB/%dB)\n",
		m.Name, m.Version, m.APIVersion, m.Runtime, len(m.Match),
		m.Limits.TimeoutMs, m.Limits.MemoryMB, m.Limits.MaxResponseBytes)
	return nil
}

func pluginTest(args []string) error {
	fs := flag.NewFlagSet("plugin test", flag.ContinueOnError)
	keyPath := fs.String("key", "", "file containing one or more base64 Ed25519 public keys")
	fixture := fs.String("fixture", "", "recorded replay fixture (cases.json)")
	insecure := fs.Bool("insecure-plugins", insecurePlugins, "skip signature verification (local dev)")
	if err := fs.Parse(reorderFlags(args)); err != nil {
		return err
	}
	if fs.NArg() != 1 || *fixture == "" {
		return fmt.Errorf("用法: udctl plugin test <dir> --fixture <file> [--key <file>] [--insecure-plugins]")
	}
	keys, err := loadKeys(*keyPath, *insecure)
	if err != nil {
		return err
	}
	results, err := plugin.RunFixture(fs.Arg(0), *fixture, keys, true, *insecure)
	if err != nil {
		return err
	}
	failed := 0
	for _, r := range results {
		status := "PASS"
		if !r.OK {
			status, failed = "FAIL", failed+1
		}
		fmt.Printf("[%s] %s: %s\n", status, r.URL, r.Message)
	}
	if failed > 0 {
		return fmt.Errorf("%d/%d fixture cases failed", failed, len(results))
	}
	fmt.Printf("全部 %d 个回放用例通过（未发起任何真实网络请求）\n", len(results))
	return nil
}

func pluginList(args []string) error {
	fs := flag.NewFlagSet("plugin list", flag.ContinueOnError)
	keyPath := fs.String("key", "", "file containing one or more base64 Ed25519 public keys")
	insecure := fs.Bool("insecure-plugins", insecurePlugins, "skip signature verification (local dev)")
	if err := fs.Parse(reorderFlags(args)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("用法: udctl plugin list <dir> [--key <file>] [--insecure-plugins]")
	}
	keys, err := loadKeys(*keyPath, *insecure)
	if err != nil {
		return err
	}
	mgr, err := plugin.New(plugin.Options{
		Dir: fs.Arg(0), TrustedKeys: keys, VerifySignature: !*insecure, Insecure: *insecure,
	})
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(mgr.Plugins(), "", "  ")
	fmt.Println(string(b))
	return nil
}

func loadKeys(path string, insecure bool) ([]ed25519.PublicKey, error) {
	if insecure {
		return nil, nil
	}
	if path == "" {
		return nil, fmt.Errorf("缺少 --key <公钥文件>（或使用 --insecure-plugins 仅限本地开发）")
	}
	lines, err := readKeyLines(path)
	if err != nil {
		return nil, err
	}
	return plugin.ParsePublicKeys(lines)
}

func readPrivateKey(path string) (string, error) {
	lines, err := readKeyLines(path)
	if err != nil {
		return "", err
	}
	if len(lines) != 1 {
		return "", fmt.Errorf("%s 必须只含一行 base64 私钥", path)
	}
	return lines[0], nil
}

func readKeyLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "public:"), "private:"))
		out = append(out, line)
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s 不含任何密钥", path)
	}
	return out, nil
}

const scaffoldManifest = `{
  "name": "site.example",
  "version": "1.0.0",
  "apiVersion": "ud.plugin/v1",
  "type": "resolver",
  "runtime": "goja",
  "entry": "index.js",
  "match": ["^https?://([^/]+\\.)?example\\.com/video/"],
  "capabilities": {
    "http": { "allowedHosts": ["example.com", "cdn.example.com"], "maxRedirects": 3 },
    "fs": false,
    "env": false,
    "subprocess": false,
    "crypto": ["base64", "sha256"]
  },
  "limits": { "timeoutMs": 10000, "memoryMB": 32, "maxResponseBytes": 1048576 },
  "author": "local",
  "license": "MIT"
}
`

const scaffoldScript = `// ES5.1 only. The host rewrites ` + "`export`" + ` declarations into an ES5 IIFE.
export const meta = {
  name: "site.example",
  version: "1.0.0"
};

export function resolve(url, ctx) {
  var html = ctx.http.get(url, { headers: { "User-Agent": "UD/1.0" } }).body;
  var m = /"playUrl":"([^"]+)"/.exec(html);
  if (!m) throw new Error("RULE_OUTDATED: playUrl not found");
  return {
    files: [{ index: 0, path: "video.mp4", selected: true }],
    mirrors: [{ url: unescape(m[1]), priority: 1 }],
    headers: { "Referer": url },
    identity: { isAdaptive: false }
  };
}
`

const scaffoldFixture = `{
  "responses": {
    "https://example.com/video/1": {
      "status": 200,
      "body": "{\"playUrl\":\"https%3A%2F%2Fcdn.example.com%2Fv%2F1.mp4\"}"
    }
  },
  "cases": [
    {
      "url": "https://example.com/video/1",
      "expect": {
        "mirrors": ["https://cdn.example.com/v/1.mp4"],
        "files": ["video.mp4"]
      }
    },
    {
      "url": "https://example.com/video/2",
      "expect": { "error": "RULE_OUTDATED" }
    }
  ]
}
`
