package core

import (
	"encoding/json"
	"reflect"
	"testing"
)

// T-FR-RSV-06-01: the four Resource shapes of docs/06 §4 must round-trip
// losslessly. ADR-0013 makes this the highest-value P0 contract test.
func TestResourceJSONRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   Resource
	}{
		{
			name: "single-http-multi-mirror",
			in: Resource{
				ID: "01J8Z", Kind: KindSingle, Engine: EngineGo,
				Files: []ResourceFile{{Index: 0, Path: "ubuntu.iso", Size: 2147483648, Selected: true}},
				Mirrors: []Mirror{
					{URL: "https://mirror.a/ubuntu.iso", Priority: 1, Weight: 1},
					{URL: "https://mirror.b/ubuntu.iso", Priority: 2, Geo: "eu", Failures: 3},
				},
				Checksum:   &Checksum{Algo: "sha256", Whole: "9f86", Pieces: []string{"aa", "bb"}},
				Credential: "keychain://ud/cred/2f1c",
			},
		},
		{
			name: "torrent-multi-file-private-dual-infohash",
			in: Resource{
				ID: "01J8Y", Kind: KindTorrent, Engine: EngineGo,
				Files: []ResourceFile{
					{Index: 0, Path: "Series/S01E01.mkv", Size: 1073741824, Selected: true},
					{Index: 1, Path: "Series/S01E02.mkv", Size: 1073741824, Selected: false},
				},
				Identity: ProtocolIdentity{
					InfoHashV1: "A1B2", InfoHashV2: "C3D4", Private: true,
					Trackers:   [][]string{{"https://tracker.example/announce/<passkey_ref>"}},
					PasskeyRef: "keychain://ud/passkey/8a", PeerID: "-GT0001-",
				},
			},
		},
		{
			name: "ed2k",
			in: Resource{
				ID: "01J8X", Kind: KindED2K, Engine: EngineGo,
				Files: []ResourceFile{{Index: 0, Path: "old-archive.rar", Size: 734003200, Selected: true}},
				Identity: ProtocolIdentity{
					ED2KFileHash: "31D6", AICHRoot: "FE", UserHash: "9C",
					IdentKeyRef: "file://~/.local/share/ud/ed2k/ident.pem",
					CreditsRef:  "file://~/.local/share/ud/ed2k/clients.met",
				},
			},
		},
		{
			name: "adaptive-stream",
			in: Resource{
				ID: "01J8W", Kind: KindStream, Engine: EngineGo,
				Files: []ResourceFile{
					{Index: 0, Path: "video.mp4", Selected: true},
					{Index: 1, Path: "audio.m4a", Selected: true},
				},
				Headers:     map[string]string{"Referer": "https://example.com"},
				Identity:    ProtocolIdentity{VariantID: "1080p", IsAdaptive: true},
				PostProcess: []ProcessSpec{{Type: "remux", Target: "output.mp4"}},
				Limits:      TaskLimits{MaxConnections: 16, ChunkSize: 4 << 20},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b, err := json.Marshal(c.in)
			if err != nil {
				t.Fatal(err)
			}
			var got Resource
			if err := json.Unmarshal(b, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(c.in, got) {
				t.Fatalf("round trip mismatch\nwant: %+v\ngot:  %+v\njson: %s", c.in, got, b)
			}
			if string(b) == "" {
				t.Fatal("empty json")
			}
		})
	}
}

// T-FR-RSV-06-02: raw torrent metainfo must never leak into JSON (docs/06 §6).
func TestTorrentMetaNotSerialized(t *testing.T) {
	b, _ := json.Marshal(Resource{
		ID: "x", Kind: KindTorrent, Identity: ProtocolIdentity{TorrentMeta: []byte("SECRET-META")},
	})
	if contains(string(b), "SECRET-META") || contains(string(b), "TorrentMeta") {
		t.Fatalf("metainfo leaked: %s", b)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestParseInputClassification(t *testing.T) {
	cases := []struct {
		raw  string
		kind InputKind
	}{
		{"https://example.com/a.iso", InputURL},
		{"magnet:?xt=urn:btih:ABCD", InputMagnet},
		{"ed2k://|file|a.rar|1|ABC|/", InputED2K},
		{"thunder://QUFodHRwWlo=", InputThunder},
		{"flashget://ABC", InputThunder},
		{"/tmp/a.torrent", InputTorrent},
		{"/tmp/a.meta4", InputMetalink},
		{"example.com/file.zip", InputURL},
		{"https://t.me/channel/123", InputTelegram},
	}
	for _, c := range cases {
		in, err := ParseInput(c.raw)
		if err != nil {
			t.Fatalf("%s: %v", c.raw, err)
		}
		if in.Kind != c.kind {
			t.Errorf("%s: got %s want %s", c.raw, in.Kind, c.kind)
		}
	}
	for _, bad := range []string{"", "file:///etc/passwd", "gopher://x"} {
		if _, err := ParseInput(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}
