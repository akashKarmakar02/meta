package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

type supervisedFake struct {
	done chan struct{}
	once sync.Once
	err  error
}

func (p *supervisedFake) Wait() error            { <-p.done; return p.err }
func (p *supervisedFake) Signal(os.Signal) error { p.once.Do(func() { close(p.done) }); return nil }
func (p *supervisedFake) Kill() error            { return p.Signal(os.Kill) }

type twoRunner struct {
	vite, air *supervisedFake
	calls     chan string
	fail      string
	contexts  []context.Context
}

func (r *twoRunner) Start(ctx context.Context, _ io.Writer, name string, _ ...string) (process, error) {
	r.contexts = append(r.contexts, ctx)
	r.calls <- name
	if r.fail == name {
		return nil, errors.New("start failed")
	}
	if name == "node" {
		return r.vite, nil
	}
	return r.air, nil
}
func TestDevSupervisesViteAndAir(t *testing.T) {
	for _, exit := range []string{"signal", "vite", "air", "air-start"} {
		t.Run(exit, func(t *testing.T) {
			root := makeFullProject(t)
			t.Chdir(root)
			file := filepath.Join(root, "node_modules/vite/bin/vite.js")
			if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(file, nil, 0644); err != nil {
				t.Fatal(err)
			}
			v := &supervisedFake{done: make(chan struct{})}
			a := &supervisedFake{done: make(chan struct{})}
			runner := &twoRunner{vite: v, air: a, calls: make(chan string, 2)}
			if exit == "air-start" {
				runner.fail = "air"
			}
			sigs := make(chan os.Signal, 1)
			done := make(chan error, 1)
			go func() {
				done <- runDevWith(&Context{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}, devDeps{lookAir: func() (string, error) { return "air", nil }, lookNode: func() (string, error) { return "node", nil }, runner: runner, signals: func() (<-chan os.Signal, func()) { return sigs, func() {} }, grace: time.Second})
			}()
			if got := <-runner.calls; got != "node" {
				t.Fatalf("first process: %s", got)
			}
			if got := <-runner.calls; got != "air" {
				t.Fatalf("second process: %s", got)
			}
			switch exit {
			case "signal":
				sigs <- syscall.SIGINT
			case "vite":
				v.err = errors.New("vite crashed")
				_ = v.Signal(os.Interrupt)
			case "air":
				a.err = errors.New("air crashed")
				_ = a.Signal(os.Interrupt)
			}
			select {
			case err := <-done:
				if exit == "signal" && err != nil {
					t.Fatal(err)
				}
				if exit != "signal" && (err == nil || !strings.Contains(err.Error(), map[string]string{"vite": "Vite", "air": "Air", "air-start": "Air"}[exit])) {
					t.Fatalf("unexpected error: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("supervisor did not stop")
			}
			select {
			case <-v.done:
			default:
				t.Error("Vite still running")
			}
			if exit != "air-start" {
				select {
				case <-a.done:
				default:
					t.Error("Air still running")
				}
			}
			if env, ok := runner.contexts[1].Value(ctxKeyEnv).([]string); !ok || len(env) != 1 || !strings.HasPrefix(env[0], "GO_META_VITE_ORIGIN=http://127.0.0.1:") {
				t.Errorf("Air environment: %v", env)
			}
		})
	}
}

func TestDevViteStartupFailure(t *testing.T) {
	root := makeFullProject(t)
	t.Chdir(root)
	file := filepath.Join(root, "node_modules/vite/bin/vite.js")
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, nil, 0644); err != nil {
		t.Fatal(err)
	}
	err := runDevWith(&Context{Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}}, devDeps{
		lookAir:  func() (string, error) { return "air", nil },
		lookNode: func() (string, error) { return "node", nil },
		runner:   failRunner{}, signals: immediateSignals, grace: time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "failed to start Vite") {
		t.Fatalf("unexpected error: %v", err)
	}
}
