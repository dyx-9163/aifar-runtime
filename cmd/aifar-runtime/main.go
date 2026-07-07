package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"aifar-runtime/internal/runtimeagent"
)

const defaultAPIAddr = runtimeagent.DefaultAPIListen

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	if err := run(os.Args[1], os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(command string, args []string) error {
	switch command {
	case "health":
		cmd := flag.NewFlagSet("health", flag.ExitOnError)
		configPath := cmd.String("config", "", "path to runtime config yaml")
		_ = cmd.Parse(args)
		config, err := runtimeagent.LoadRuntimeConfig(*configPath)
		if err != nil {
			return err
		}
		if err := dockerHealth(context.Background(), config); err != nil {
			return err
		}
		fmt.Println(`{"status":"ok"}`)
		return nil
	case "validate":
		cmd := flag.NewFlagSet("validate", flag.ExitOnError)
		file := cmd.String("f", "", "path to rendered-runtime.yaml")
		_ = cmd.Parse(args)
		if strings.TrimSpace(*file) == "" {
			return errors.New("-f is required")
		}
		runtime, err := readRuntimeFile(*file)
		if err != nil {
			return err
		}
		writeStdoutJSON(map[string]any{"status": "valid", "runtime": runtime.Metadata})
		return nil
	case "apply":
		cmd := flag.NewFlagSet("apply", flag.ExitOnError)
		file := cmd.String("f", "", "path to rendered-runtime.yaml")
		addr := cmd.String("addr", defaultAPIAddr, "runtime API address")
		_ = cmd.Parse(args)
		if strings.TrimSpace(*file) == "" {
			return errors.New("-f is required")
		}
		runtime, err := readRuntimeFile(*file)
		if err != nil {
			return err
		}
		if err := putRuntime(context.Background(), *addr, runtime); err != nil {
			return err
		}
		writeStdoutJSON(map[string]any{"status": "applied", "runtime": runtime.Metadata})
		return nil
	case "status":
		cmd := flag.NewFlagSet("status", flag.ExitOnError)
		addr := cmd.String("addr", defaultAPIAddr, "runtime API address")
		namespace := cmd.String("namespace", runtimeagent.DefaultNamespace, "runtime namespace")
		name := cmd.String("name", "", "runtime name")
		_ = cmd.Parse(args)
		var data []byte
		var err error
		if strings.TrimSpace(*name) == "" {
			data, err = getRuntimeStatus(context.Background(), *addr)
		} else {
			data, err = getRuntimeResource(context.Background(), *addr, *namespace, *name, "status")
		}
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	case "events":
		cmd := flag.NewFlagSet("events", flag.ExitOnError)
		addr := cmd.String("addr", defaultAPIAddr, "runtime API address")
		namespace := cmd.String("namespace", runtimeagent.DefaultNamespace, "runtime namespace")
		name := cmd.String("name", "", "runtime name")
		tail := cmd.Int("tail", 100, "event tail count")
		_ = cmd.Parse(args)
		if strings.TrimSpace(*name) == "" {
			return errors.New("--name is required")
		}
		data, err := getRuntimeEvents(context.Background(), *addr, *namespace, *name, *tail)
		if err != nil {
			return err
		}
		fmt.Println(string(data))
		return nil
	case "delete":
		cmd := flag.NewFlagSet("delete", flag.ExitOnError)
		addr := cmd.String("addr", defaultAPIAddr, "runtime API address")
		namespace := cmd.String("namespace", runtimeagent.DefaultNamespace, "runtime namespace")
		name := cmd.String("name", "", "runtime name")
		_ = cmd.Parse(args)
		if strings.TrimSpace(*name) == "" {
			return errors.New("--name is required")
		}
		if err := deleteRuntime(context.Background(), *addr, *namespace, *name); err != nil {
			return err
		}
		writeStdoutJSON(map[string]any{"status": "deleted", "namespace": *namespace, "name": *name})
		return nil
	case "serve":
		cmd := flag.NewFlagSet("serve", flag.ExitOnError)
		configPath := cmd.String("config", "", "path to runtime config yaml")
		listen := cmd.String("listen", "", "runtime API listen address override")
		addr := cmd.String("addr", "", "runtime API listen address alias")
		stateDir := cmd.String("state-dir", "", "runtime state directory override")
		_ = cmd.Parse(args)
		config, err := runtimeagent.LoadRuntimeConfig(*configPath)
		if err != nil {
			return err
		}
		if strings.TrimSpace(*addr) != "" {
			*listen = *addr
		}
		if strings.TrimSpace(*listen) != "" {
			config.API.Listen = strings.TrimSpace(*listen)
		}
		if strings.TrimSpace(*stateDir) != "" {
			config.State.Dir = strings.TrimSpace(*stateDir)
		}
		return serve(config)
	case "reconcile-runtime", "reconcile-ingress", "reconcile":
		cmd := flag.NewFlagSet(command, flag.ExitOnError)
		specPath := cmd.String("spec", "", "path to runtime spec json/yaml")
		addr := cmd.String("addr", defaultAPIAddr, "runtime API address")
		_ = cmd.Parse(args)
		if strings.TrimSpace(*specPath) == "" {
			return errors.New("--spec is required")
		}
		runtime, err := readRuntimeFile(*specPath)
		if err != nil {
			return err
		}
		if err := postLegacyReconcile(context.Background(), *addr, runtime); err != nil {
			return err
		}
		writeStdoutJSON(map[string]any{"status": "reconciled", "runtime": runtime.Metadata})
		return nil
	case "remove-instance":
		cmd := flag.NewFlagSet("remove-instance", flag.ExitOnError)
		instance := cmd.String("instance", "default", "legacy runtime instance id")
		addr := cmd.String("addr", defaultAPIAddr, "runtime API address")
		_ = cmd.Parse(args)
		return deleteRuntime(context.Background(), *addr, runtimeagent.DefaultNamespace, *instance)
	case "register-nacos", "register-nacos-proxies", "deregister-nacos", "deregister-nacos-proxies":
		return errors.New("unsupported: AIFAR Runtime v0.1 does not register services into Nacos")
	default:
		usage()
		return fmt.Errorf("unknown command %q", command)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: aifar-runtime serve [--config /etc/aifar-runtime/config.yaml] [--listen 127.0.0.1:18081] [--state-dir /var/lib/aifar-runtime]")
	fmt.Fprintln(os.Stderr, "       aifar-runtime health [--config /etc/aifar-runtime/config.yaml]")
	fmt.Fprintln(os.Stderr, "       aifar-runtime validate -f rendered-runtime.yaml")
	fmt.Fprintln(os.Stderr, "       aifar-runtime apply -f rendered-runtime.yaml [--addr 127.0.0.1:18081]")
	fmt.Fprintln(os.Stderr, "       aifar-runtime status [--namespace default --name demo] [--addr 127.0.0.1:18081]")
	fmt.Fprintln(os.Stderr, "       aifar-runtime events --namespace default --name demo [--tail 100] [--addr 127.0.0.1:18081]")
	fmt.Fprintln(os.Stderr, "       aifar-runtime delete --namespace default --name demo [--addr 127.0.0.1:18081]")
}

func readRuntimeFile(path string) (runtimeagent.Runtime, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return runtimeagent.Runtime{}, err
	}
	return runtimeagent.ParseRuntimeDocument(data)
}

func dockerHealth(ctx context.Context, config runtimeagent.RuntimeConfig) error {
	config = runtimeagent.NormalizeRuntimeConfig(config)
	if err := runtimeagent.ValidateRuntimeConfig(config); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, config.Health.DockerTimeout.Duration)
	defer cancel()
	_, err := runtimeagent.ExecRunner{}.Run(ctx, config.Docker.Command, "info")
	return err
}

func serve(config runtimeagent.RuntimeConfig) error {
	config = runtimeagent.NormalizeRuntimeConfig(config)
	if err := runtimeagent.ValidateRuntimeConfig(config); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	manager := runtimeagent.NewManager(runtimeagent.ManagerOptions{Config: config, Log: os.Stdout})
	if err := manager.Load(ctx); err != nil {
		return err
	}
	go manager.StartRuntimeResync(ctx, config.Reconcile.Interval.Duration)
	go manager.StartDockerEventSync(ctx, config.Docker.EventDebounce.Duration)
	server := &http.Server{
		Addr:              config.API.Listen,
		Handler:           newRuntimeHandler(manager, manager.Ready),
		ReadHeaderTimeout: config.API.ReadHeaderTimeout.Duration,
	}
	serverErr := make(chan error, 1)
	go func() {
		log.Printf("aifar-runtime listening on %s", config.API.Listen)
		err := server.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	select {
	case err := <-serverErr:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), config.API.ShutdownTimeout.Duration)
	defer cancel()
	stop()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown runtime API: %w", err)
	}
	if err := manager.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-serverErr; err != nil {
		return err
	}
	log.Print("aifar-runtime stopped")
	return nil
}

func newRuntimeHandler(manager *runtimeagent.Manager, readyCheck func(context.Context) error) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := readyCheck(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := readyCheck(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, manager.Status())
	})
	mux.HandleFunc("/apis/aifar.io/v1/namespaces/", func(w http.ResponseWriter, r *http.Request) {
		handleRuntimeAPI(manager, w, r)
	})
	mux.HandleFunc("/runtime/reconcile", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		runtime, err := decodeRuntimeRequest(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := (runtimeagent.Reconciler{Manager: manager, Log: os.Stdout}).ReconcileRuntime(r.Context(), runtime); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "reconciled", "runtime": runtimeagent.KeyForRuntime(runtime)})
	})
	mux.HandleFunc("/runtime/instances/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		instance := strings.Trim(strings.TrimPrefix(r.URL.Path, "/runtime/instances/"), "/")
		if instance == "" {
			http.Error(w, "instance is required", http.StatusBadRequest)
			return
		}
		if err := manager.Remove(r.Context(), runtimeagent.DefaultNamespace, instance); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
	})
	return mux
}

func handleRuntimeAPI(manager *runtimeagent.Manager, w http.ResponseWriter, r *http.Request) {
	namespace, name, subresource, validate, ok := parseRuntimeAPIPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	switch {
	case validate:
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		runtime, err := decodeRuntimeRequest(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := requirePathMatchesRuntime(namespace, name, runtime); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "valid", "runtime": runtimeagent.KeyForRuntime(runtime)})
	case subresource == "" && r.Method == http.MethodPut:
		runtime, err := decodeRuntimeRequest(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := requirePathMatchesRuntime(namespace, name, runtime); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := manager.Apply(r.Context(), runtime); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "applied", "runtime": runtimeagent.KeyForRuntime(runtime)})
	case subresource == "" && r.Method == http.MethodGet:
		runtime, status, found := manager.GetRuntime(namespace, name)
		if !found {
			http.NotFound(w, r)
			return
		}
		runtime.Status = &status
		writeJSON(w, http.StatusOK, runtime)
	case subresource == "status" && r.Method == http.MethodGet:
		_, status, found := manager.GetRuntime(namespace, name)
		if !found {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, http.StatusOK, status)
	case subresource == "events" && r.Method == http.MethodGet:
		tail, _ := strconv.Atoi(r.URL.Query().Get("tail"))
		events, err := manager.Events(namespace, name, tail)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, events)
	case subresource == "" && r.Method == http.MethodDelete:
		if err := manager.Remove(r.Context(), namespace, name); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func parseRuntimeAPIPath(path string) (namespace, name, subresource string, validate bool, ok bool) {
	prefix := "/apis/aifar.io/v1/namespaces/"
	rest := strings.TrimPrefix(path, prefix)
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) < 3 || parts[1] != "runtimes" {
		return "", "", "", false, false
	}
	namespace = parts[0]
	name = parts[2]
	if rawName, suffix, found := strings.Cut(name, ":"); found {
		name = rawName
		validate = suffix == "validate"
		if !validate {
			return "", "", "", false, false
		}
	}
	if len(parts) > 3 {
		subresource = parts[3]
	}
	return namespace, name, subresource, validate, namespace != "" && name != ""
}

func decodeRuntimeRequest(r *http.Request) (runtimeagent.Runtime, error) {
	defer r.Body.Close()
	data, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		return runtimeagent.Runtime{}, err
	}
	return runtimeagent.ParseRuntimeDocument(data)
}

func requirePathMatchesRuntime(namespace, name string, runtime runtimeagent.Runtime) error {
	key := runtimeagent.KeyForRuntime(runtime)
	if key.Namespace != namespace || key.Name != name {
		return fmt.Errorf("request path %s/%s does not match runtime %s", namespace, name, key.String())
	}
	return nil
}

func putRuntime(ctx context.Context, addr string, runtime runtimeagent.Runtime) error {
	return doJSON(ctx, http.MethodPut, runtimeResourceURL(addr, runtime.Metadata.Namespace, runtime.Metadata.Name, ""), runtime, nil)
}

func postLegacyReconcile(ctx context.Context, addr string, runtime runtimeagent.Runtime) error {
	return doJSON(ctx, http.MethodPost, "http://"+addr+"/runtime/reconcile", runtime, nil)
}

func deleteRuntime(ctx context.Context, addr, namespace, name string) error {
	return doJSON(ctx, http.MethodDelete, runtimeResourceURL(addr, namespace, name, ""), nil, nil)
}

func getRuntimeStatus(ctx context.Context, addr string) ([]byte, error) {
	return getURL(ctx, "http://"+addr+"/status")
}

func getRuntimeResource(ctx context.Context, addr, namespace, name, subresource string) ([]byte, error) {
	return getURL(ctx, runtimeResourceURL(addr, namespace, name, subresource))
}

func getRuntimeEvents(ctx context.Context, addr, namespace, name string, tail int) ([]byte, error) {
	url := runtimeResourceURL(addr, namespace, name, "events")
	if tail > 0 {
		url += "?tail=" + strconv.Itoa(tail)
	}
	return getURL(ctx, url)
}

func runtimeResourceURL(addr, namespace, name, subresource string) string {
	path := "http://" + addr + "/apis/aifar.io/v1/namespaces/" + namespace + "/runtimes/" + name
	if strings.TrimSpace(subresource) != "" {
		path += "/" + subresource
	}
	return path
}

func doJSON(ctx context.Context, method, url string, in any, out any) error {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("aifar-runtime service is not reachable on %s: %w", url, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("aifar-runtime request failed: %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

func getURL(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("aifar-runtime service is not reachable on %s: %w", url, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("aifar-runtime request failed: %s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	return data, nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeStdoutJSON(value any) {
	_ = json.NewEncoder(os.Stdout).Encode(value)
}
