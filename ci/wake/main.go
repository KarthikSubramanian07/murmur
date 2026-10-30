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
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"slices"
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

// start starts the instance unless it is running already, and returns the state it found. An
// instance that is stopping can't be started until it has stopped, which takes a few seconds, so it
// waits for that, up to two minutes: a job queued while the VM stops itself would otherwise wait for
// the next job to wake it.
func start(ctx context.Context, instance string) (string, error) {
	base := "https://compute.googleapis.com/compute/v1/" + instance
	deadline := time.Now().Add(2 * time.Minute)
	for {
		var inst struct {
			Status string `json:"status"`
		}
		if err := call(ctx, "GET", base, &inst); err != nil {
			return "", err
		}
		switch inst.Status {
		case "RUNNING", "PROVISIONING", "STAGING", "REPAIRING":
			return inst.Status, nil
		case "TERMINATED", "STOPPED", "SUSPENDED":
			op := base + "/start"
			if inst.Status == "SUSPENDED" {
				op = base + "/resume"
			}
			return inst.Status, call(ctx, "POST", op, nil)
		}
		if time.Now().After(deadline) { // STOPPING or SUSPENDING for too long
			return inst.Status, fmt.Errorf("still %s after two minutes", inst.Status)
		}
		select {
		case <-ctx.Done():
			return inst.Status, ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

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
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s %s: %s: %s", method, url, resp.Status, b)
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

// token is an access token for the service account, from the metadata server.
func token(ctx context.Context) (string, error) {
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
