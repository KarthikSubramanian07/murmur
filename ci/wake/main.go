// Command wake starts the Linux CI runner's VM when GitHub queues a job for it.
//
// The runner (ci/linux-runner) runs on a Spot VM that stops itself after 30 minutes without a job,
// so it bills only its disk while nobody is pushing. GitHub sends this service a workflow_job event
// for every job in the repo; for a queued job that asks for the runner's label, it starts the VM,
// which takes the job once it has booted. It runs on Cloud Run, inside its free tier. docs/testing.md
// says how it is deployed.
//
// Environment: WEBHOOK_SECRET (the webhook's secret, which signs each delivery), INSTANCE
// (projects/P/zones/Z/instances/NAME), LABEL (the runner's label, t4-ci-linux) and PORT (set by
// Cloud Run).
package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

func main() {
	secret, instance, label := os.Getenv("WEBHOOK_SECRET"), os.Getenv("INSTANCE"), os.Getenv("LABEL")
	if secret == "" || instance == "" || label == "" {
		log.Fatal("WEBHOOK_SECRET, INSTANCE and LABEL must be set")
	}
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil || !signed(secret, body, r.Header.Get("X-Hub-Signature-256")) {
			http.Error(w, "bad signature", http.StatusUnauthorized)
			return
		}
		if r.Header.Get("X-GitHub-Event") != "workflow_job" {
			fmt.Fprintln(w, "ignored")
			return
		}
		var ev struct {
			Action string `json:"action"`
			Job    struct {
				Labels []string `json:"labels"`
				Name   string   `json:"name"`
				RunID  int64    `json:"run_id"`
			} `json:"workflow_job"`
		}
		if err := json.Unmarshal(body, &ev); err != nil {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		if ev.Action != "queued" || !slices.Contains(ev.Job.Labels, label) {
			fmt.Fprintln(w, "not for this runner")
			return
		}
		// GitHub gives up on a delivery after 10 s, but this goes on until the VM is starting: the
		// result matters, not GitHub's record of the delivery. So the work has its own deadline, and
		// GitHub hanging up doesn't cancel it.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 150*time.Second)
		defer cancel()
		state, err := start(ctx, instance)
		if err != nil {
			log.Printf("run %d job %q: starting %s: %v", ev.Job.RunID, ev.Job.Name, instance, err)
			http.Error(w, "start failed", http.StatusBadGateway)
			return
		}
		log.Printf("run %d job %q: %s was %s", ev.Job.RunID, ev.Job.Name, instance, state)
		fmt.Fprintf(w, "instance was %s\n", state)
	})
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

// signed says whether sig is GitHub's HMAC-SHA256 of body under secret ("sha256=<hex>").
func signed(secret string, body []byte, sig string) bool {
	got, err := hex.DecodeString(strings.TrimPrefix(sig, "sha256="))
	if err != nil || !strings.HasPrefix(sig, "sha256=") {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(got, mac.Sum(nil))
}

// The Compute Engine API, how often start looks again, and where tokens come from; tests change
// them.
var (
	computeAPI = "https://compute.googleapis.com/compute/v1/"
	every      = 5 * time.Second
	token      = metadataToken
)

// start makes sure the instance is booting, or running and not about to stop, and returns the state
// it found. Until ctx ends it waits for an instance that is stopping, and tries again after a start
// that failed: a Spot VM can find no capacity.
//
// An instance that stops itself shows RUNNING until its system has shut down, so t4-idle-stop first
// writes the guest attribute t4/stopping (the time, in Unix seconds), and deletes it if a job arrives
// after all. A mark from before the instance's last start is left from an earlier stop.
func start(ctx context.Context, instance string) (string, error) {
	base := computeAPI + instance
	found := ""
	var failed error // the last start's, which says more than running out of time
	why := func(err error) error {
		if failed != nil && ctx.Err() != nil {
			return fmt.Errorf("gave up; the last start failed: %w", failed)
		}
		return err
	}
	for {
		var inst struct {
			Status    string `json:"status"`
			LastStart string `json:"lastStartTimestamp"`
		}
		if err := call(ctx, "GET", base, &inst); err != nil {
			return found, why(err)
		}
		if found == "" {
			found = inst.Status
		}
		switch inst.Status {
		case "PROVISIONING", "STAGING", "REPAIRING":
			return found, nil
		case "RUNNING":
			stopping, err := markedStopping(ctx, base, inst.LastStart)
			if err != nil {
				return found, why(err)
			}
			if !stopping {
				return found, nil
			}
			if found == "RUNNING" {
				found = "stopping"
			}
		case "TERMINATED", "STOPPED", "SUSPENDED":
			op := base + "/start"
			if inst.Status == "SUSPENDED" {
				op = base + "/resume"
			}
			err := run(ctx, op)
			if err == nil {
				return found, nil
			}
			if ctx.Err() == nil {
				failed = err
				log.Printf("starting %s: %v; trying again", instance, err)
			}
		}
		select {
		case <-ctx.Done():
			return found, why(fmt.Errorf("still %s: %w", inst.Status, ctx.Err()))
		case <-time.After(every):
		}
	}
}

// markedStopping says whether the instance's t4/stopping mark is newer than its last start.
func markedStopping(ctx context.Context, base, lastStart string) (bool, error) {
	var attrs struct {
		Value struct {
			Items []struct {
				Value string `json:"value"`
			} `json:"items"`
		} `json:"queryValue"`
	}
	err := call(ctx, "GET", base+"/getGuestAttributes?queryPath=t4/stopping", &attrs)
	if errors.Is(err, errNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	started, err := time.Parse(time.RFC3339Nano, lastStart)
	if err != nil {
		return false, nil // no start to compare with; better to start too early than wait for nothing
	}
	for _, it := range attrs.Value.Items {
		if at, err := strconv.ParseInt(it.Value, 10, 64); err == nil && time.Unix(at, 0).After(started) {
			return true, nil
		}
	}
	return false, nil
}

// run makes a request that returns an operation, and waits for the operation to finish: a start is
// accepted at once and can fail afterwards.
func run(ctx context.Context, url string) error {
	var op struct {
		Status   string `json:"status"`
		SelfLink string `json:"selfLink"`
		Error    *struct {
			Errors []struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"errors"`
		} `json:"error"`
	}
	if err := call(ctx, "POST", url, &op); err != nil {
		return err
	}
	for op.Status != "DONE" {
		if op.SelfLink == "" {
			return fmt.Errorf("POST %s: an operation with no selfLink", url)
		}
		// wait returns when the operation is done, or after about two minutes.
		if err := call(ctx, "POST", op.SelfLink+"/wait", &op); err != nil {
			return err
		}
	}
	if op.Error != nil {
		var msgs []string
		for _, e := range op.Error.Errors {
			msgs = append(msgs, e.Code+": "+e.Message)
		}
		return fmt.Errorf("POST %s: %s", url, strings.Join(msgs, "; "))
	}
	return nil
}

var errNotFound = errors.New("not found")

// call makes a Compute Engine API request as the service's own service account, decoding the reply
// into out if it isn't nil.
func call(ctx context.Context, method, url string, out any) error {
	tok, err := token(ctx)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%s %s: %w", method, url, errNotFound)
	}
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s %s: %s: %s", method, url, resp.Status, b)
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

// metadataToken is an access token for the service account, from the metadata server.
func metadataToken(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET",
		"http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Metadata-Flavor", "Google")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var t struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return "", err
	}
	if t.AccessToken == "" {
		return "", fmt.Errorf("no access token from the metadata server (%s)", resp.Status)
	}
	return t.AccessToken, nil
}
