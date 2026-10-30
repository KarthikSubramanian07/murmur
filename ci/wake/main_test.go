package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSigned(t *testing.T) {
	body := []byte(`{"action":"queued"}`)
	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write(body)
	good := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	cases := []struct {
		name, secret, sig string
		body              []byte
		want              bool
	}{
		{"right secret", "s3cret", good, body, true},
		{"wrong secret", "other", good, body, false},
		{"changed body", "s3cret", good, []byte(`{"action":"completed"}`), false},
		{"no prefix", "s3cret", good[len("sha256="):], body, false},
		{"empty", "s3cret", "", body, false},
		{"not hex", "s3cret", "sha256=zz", body, false},
	}
	for _, c := range cases {
		if got := signed(c.secret, c.body, c.sig); got != c.want {
			t.Errorf("%s: signed = %v, want %v", c.name, got, c.want)
		}
	}
}

// fake is the part of the Compute Engine API start uses, for one instance.
type fake struct {
	mu         sync.Mutex
	srv        *httptest.Server
	statuses   []string // the instance's status at each look; the last one stays
	lastStart  string
	mark       string // t4/stopping's value, or "" for none
	failStarts int    // how many starts fail once their operation is done
	starts     int
}

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := r.URL.Path
	switch {
	case r.Method == "GET" && strings.HasSuffix(p, "/getGuestAttributes"):
		if f.mark == "" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"queryValue": map[string]any{
			"items": []any{map[string]string{"namespace": "t4", "key": "stopping", "value": f.mark}}}})
	case r.Method == "GET":
		s := f.statuses[0]
		if len(f.statuses) > 1 {
			f.statuses = f.statuses[1:]
		}
		json.NewEncoder(w).Encode(map[string]string{"status": s, "lastStartTimestamp": f.lastStart})
	case r.Method == "POST" && strings.HasSuffix(p, "/start"):
		f.starts++
		json.NewEncoder(w).Encode(map[string]string{"status": "RUNNING", "selfLink": f.srv.URL + "/op/" + strconv.Itoa(f.starts)})
	case r.Method == "POST" && strings.HasPrefix(p, "/op/") && strings.HasSuffix(p, "/wait"):
		n, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(p, "/op/"), "/wait"))
		op := map[string]any{"status": "DONE"}
		if n <= f.failStarts {
			op["error"] = map[string]any{"errors": []any{map[string]string{"code": "ZONE_RESOURCE_POOL_EXHAUSTED", "message": "no capacity"}}}
		}
		json.NewEncoder(w).Encode(op)
	default:
		http.Error(w, "unexpected "+r.Method+" "+p, http.StatusBadRequest)
	}
}

func useFake(t *testing.T, f *fake) {
	f.srv = httptest.NewServer(f)
	t.Cleanup(f.srv.Close)
	api, ev, tok := computeAPI, every, token
	computeAPI, every = f.srv.URL+"/", time.Millisecond
	token = func(context.Context) (string, error) { return "test", nil }
	t.Cleanup(func() { computeAPI, every, token = api, ev, tok })
}

const vm = "projects/p/zones/z/instances/vm"

func TestStartTriesAgainAfterAFailedStart(t *testing.T) {
	f := &fake{statuses: []string{"TERMINATED"}, failStarts: 1}
	useFake(t, f)
	state, err := start(context.Background(), vm)
	if err != nil || state != "TERMINATED" || f.starts != 2 {
		t.Fatalf("start = %q, %v after %d starts; want TERMINATED, nil after 2", state, err, f.starts)
	}
}

func TestStartSaysWhyItGaveUp(t *testing.T) {
	f := &fake{statuses: []string{"TERMINATED"}, failStarts: 1 << 30}
	useFake(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := start(ctx, vm); err == nil || !strings.Contains(err.Error(), "ZONE_RESOURCE_POOL_EXHAUSTED") {
		t.Fatalf("start's error = %v, want the operation's", err)
	}
}

func TestStartWaitsForAVMThatIsStopping(t *testing.T) {
	started := time.Now().Add(-time.Hour)
	f := &fake{
		statuses:  []string{"RUNNING", "RUNNING", "TERMINATED"},
		lastStart: started.Format(time.RFC3339Nano),
		mark:      strconv.FormatInt(time.Now().Unix(), 10),
	}
	useFake(t, f)
	state, err := start(context.Background(), vm)
	if err != nil || state != "stopping" || f.starts != 1 {
		t.Fatalf("start = %q, %v after %d starts; want stopping, nil after 1", state, err, f.starts)
	}
}

func TestStartLeavesARunningVM(t *testing.T) {
	started := time.Now().Add(-time.Minute)
	for name, mark := range map[string]string{
		"no mark":                     "",
		"a mark from an earlier stop": strconv.FormatInt(started.Add(-time.Hour).Unix(), 10),
	} {
		f := &fake{statuses: []string{"RUNNING", "TERMINATED"}, lastStart: started.Format(time.RFC3339Nano), mark: mark}
		useFake(t, f)
		state, err := start(context.Background(), vm)
		if err != nil || state != "RUNNING" || f.starts != 0 {
			t.Errorf("%s: start = %q, %v after %d starts; want RUNNING, nil after 0", name, state, err, f.starts)
		}
	}
}
