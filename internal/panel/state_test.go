package panel

import (
	"os"
	"runtime"
	"testing"
	"time"
)

func TestStateStoreRoundTrip(t *testing.T) {
	store := NewStateStore(t.TempDir())
	want := State{Version: stateVersion, Proxies: []Proxy{{ID: "line-1", Port: 20000, IPv6: "2408::1", CreatedAt: time.Now().UTC()}}, Pending: map[string]PendingOperation{"op": {ID: "op", Kind: "rotate", Candidate: "2408::2"}}}
	if err := store.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Proxies) != 1 || got.Proxies[0].Port != 20000 || got.Pending["op"].Candidate != "2408::2" {
		t.Fatalf("unexpected state: %#v", got)
	}
	info, err := os.Stat(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("state permissions are too broad: %o", info.Mode().Perm())
	}
}
