package main

import (
	"bytes"
	"context"
	"crypto/subtle"
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

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

type buildInfo struct {
	Version        string `json:"version"`
	Commit         string `json:"commit"`
	BuildDate      string `json:"buildDate"`
	RuntimeVersion string `json:"runtimeVersion"`
}

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
	case "version":
		writeStdoutJSON(currentBuildInfo())
		return nil
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
		token := cmd.String("token", defaultAPIToken(), "runtime API bearer token")
		_ = cmd.Parse(args)
		if strings.TrimSpace(*file) == "" {
			return errors.New("-f is required")
		}
		runtime, err := readRuntimeFile(*file)
		if err != nil {
			return err
		}
		if err := putRuntime(context.Background(), *addr, *token, runtime); err != nil {
			return err
		}
		writeStdoutJSON(map[string]any{"status": "applied", "runtime": runtime.Metadata})
		return nil
	case "status":
		cmd := flag.NewFlagSet("status", flag.ExitOnError)
		addr := cmd.String("addr", defaultAPIAddr, "runtime API address")
		namespace := cmd.String("namespace", runtimeagent.DefaultNamespace, "runtime namespace")
		name := cmd.String("name", "", "runtime name")
		token := cmd.String("token", defaultAPIToken(), "runtime API bearer token")
		_ = cmd.Parse(args)
		var data []byte
		var err error
		if strings.TrimSpace(*name) == "" {
			data, err = getRuntimeStatus(context.Background(), *addr, *token)
		} else {
			data, err = getRuntimeResource(context.Background(), *addr, *token, *namespace, *name, "status")
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
		token := cmd.String("token", defaultAPIToken(), "runtime API bearer token")
		_ = cmd.Parse(args)
		if strings.TrimSpace(*name) == "" {
			return errors.New("--name is required")
		}
		data, err := getRuntimeEvents(context.Background(), *addr, *token, *namespace, *name, *tail)
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
		token := cmd.String("token", defaultAPIToken(), "runtime API bearer token")
		_ = cmd.Parse(args)
		if strings.TrimSpace(*name) == "" {
			return errors.New("--name is required")
		}
		if err := deleteRuntime(context.Background(), *addr, *token, *namespace, *name); err != nil {
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
		token := cmd.String("token", defaultAPIToken(), "runtime API bearer token")
		_ = cmd.Parse(args)
		if strings.TrimSpace(*specPath) == "" {
			return errors.New("--spec is required")
		}
		runtime, err := readRuntimeFile(*specPath)
		if err != nil {
			return err
		}
		if err := postLegacyReconcile(context.Background(), *addr, *token, runtime); err != nil {
			return err
		}
		writeStdoutJSON(map[string]any{"status": "reconciled", "runtime": runtime.Metadata})
		return nil
	case "remove-instance":
		cmd := flag.NewFlagSet("remove-instance", flag.ExitOnError)
		instance := cmd.String("instance", "default", "legacy runtime instance id")
		addr := cmd.String("addr", defaultAPIAddr, "runtime API address")
		token := cmd.String("token", defaultAPIToken(), "runtime API bearer token")
		_ = cmd.Parse(args)
		return deleteRuntime(context.Background(), *addr, *token, runtimeagent.DefaultNamespace, *instance)
	case "register-nacos", "register-nacos-proxies", "deregister-nacos", "deregister-nacos-proxies":
		return errors.New("unsupported: AIFAR Runtime v0.1 does not register services into Nacos")
	default:
		usage()
		return fmt.Errorf("unknown command %q", command)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: aifar-runtime serve [--config /etc/aifar-runtime/config.yaml] [--listen 127.0.0.1:18081] [--state-dir /var/lib/aifar-runtime]")
	fmt.Fprintln(os.Stderr, "       aifar-runtime version")
	fmt.Fprintln(os.Stderr, "       aifar-runtime health [--config /etc/aifar-runtime/config.yaml]")
	fmt.Fprintln(os.Stderr, "       aifar-runtime validate -f rendered-runtime.yaml")
	fmt.Fprintln(os.Stderr, "       aifar-runtime apply -f rendered-runtime.yaml [--addr 127.0.0.1:18081] [--token ...]")
	fmt.Fprintln(os.Stderr, "       aifar-runtime status [--namespace default --name demo] [--addr 127.0.0.1:18081] [--token ...]")
	fmt.Fprintln(os.Stderr, "       aifar-runtime events --namespace default --name demo [--tail 100] [--addr 127.0.0.1:18081] [--token ...]")
	fmt.Fprintln(os.Stderr, "       aifar-runtime delete --namespace default --name demo [--addr 127.0.0.1:18081] [--token ...]")
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

	logOutput := io.Writer(os.Stdout)
	if config.Log.Format == "json" {
		logOutput = runtimeagent.NewStructuredLogWriter(os.Stdout)
		log.SetOutput(logOutput)
		log.SetFlags(0)
	} else {
		log.SetOutput(os.Stdout)
		log.SetFlags(log.LstdFlags)
	}

	manager := runtimeagent.NewManager(runtimeagent.ManagerOptions{Config: config, Log: logOutput})
	if err := manager.Load(ctx); err != nil {
		return err
	}
	go manager.StartRuntimeResync(ctx, config.Reconcile.Interval.Duration)
	go manager.StartDockerEventSync(ctx, config.Docker.EventDebounce.Duration)
	server := &http.Server{
		Addr: config.API.Listen,
		Handler: newRuntimeHandlerWithOptions(manager, manager.Ready, runtimeHandlerOptions{
			AuthToken:      config.Security.BearerToken,
			MetricsEnabled: config.Observability.MetricsEnabled,
			Build:          currentBuildInfo(),
		}),
		ReadHeaderTimeout: config.API.ReadHeaderTimeout.Duration,
	}
	serverErr := make(chan error, 1)
	go func() {
		log.Printf("aifar-runtime listening on %s", config.API.Listen)
		var err error
		if config.Security.TLSCertFile != "" {
			err = server.ListenAndServeTLS(config.Security.TLSCertFile, config.Security.TLSKeyFile)
		} else {
			err = server.ListenAndServe()
		}
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

type runtimeHandlerOptions struct {
	AuthToken      string
	MetricsEnabled bool
	Build          buildInfo
}

func newRuntimeHandler(manager *runtimeagent.Manager, readyCheck func(context.Context) error) http.Handler {
	return newRuntimeHandlerWithOptions(manager, readyCheck, runtimeHandlerOptions{
		MetricsEnabled: true,
		Build:          currentBuildInfo(),
	})
}

func newRuntimeHandlerWithOptions(manager *runtimeagent.Manager, readyCheck func(context.Context) error, options runtimeHandlerOptions) http.Handler {
	if options.Build.Version == "" {
		options.Build = currentBuildInfo()
	}
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
		status := manager.Status()
		status["build"] = options.Build
		writeJSON(w, http.StatusOK, status)
	})
	mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, http.StatusOK, options.Build)
	})
	if options.MetricsEnabled {
		mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			writeMetrics(w, manager, options.Build)
		})
	}
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
	return withAPISecurity(mux, options.AuthToken)
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

func putRuntime(ctx context.Context, addr, token string, runtime runtimeagent.Runtime) error {
	return doJSON(ctx, http.MethodPut, runtimeResourceURL(addr, runtime.Metadata.Namespace, runtime.Metadata.Name, ""), token, runtime, nil)
}

func postLegacyReconcile(ctx context.Context, addr, token string, runtime runtimeagent.Runtime) error {
	return doJSON(ctx, http.MethodPost, apiBaseURL(addr)+"/runtime/reconcile", token, runtime, nil)
}

func deleteRuntime(ctx context.Context, addr, token, namespace, name string) error {
	return doJSON(ctx, http.MethodDelete, runtimeResourceURL(addr, namespace, name, ""), token, nil, nil)
}

func getRuntimeStatus(ctx context.Context, addr, token string) ([]byte, error) {
	return getURL(ctx, apiBaseURL(addr)+"/status", token)
}

func getRuntimeResource(ctx context.Context, addr, token, namespace, name, subresource string) ([]byte, error) {
	return getURL(ctx, runtimeResourceURL(addr, namespace, name, subresource), token)
}

func getRuntimeEvents(ctx context.Context, addr, token, namespace, name string, tail int) ([]byte, error) {
	url := runtimeResourceURL(addr, namespace, name, "events")
	if tail > 0 {
		url += "?tail=" + strconv.Itoa(tail)
	}
	return getURL(ctx, url, token)
}

func runtimeResourceURL(addr, namespace, name, subresource string) string {
	path := apiBaseURL(addr) + "/apis/aifar.io/v1/namespaces/" + namespace + "/runtimes/" + name
	if strings.TrimSpace(subresource) != "" {
		path += "/" + subresource
	}
	return path
}

func apiBaseURL(addr string) string {
	addr = strings.TrimRight(strings.TrimSpace(addr), "/")
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return addr
	}
	return "http://" + addr
}

func doJSON(ctx context.Context, method, url, token string, in any, out any) error {
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
	setBearerToken(req, token)
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

func getURL(ctx context.Context, url, token string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	setBearerToken(req, token)
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

func setBearerToken(req *http.Request, token string) {
	token = strings.TrimSpace(token)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

func defaultAPIToken() string {
	return strings.TrimSpace(os.Getenv("AIFAR_RUNTIME_TOKEN"))
}

func currentBuildInfo() buildInfo {
	return buildInfo{
		Version:        strings.TrimSpace(version),
		Commit:         strings.TrimSpace(commit),
		BuildDate:      strings.TrimSpace(buildDate),
		RuntimeVersion: runtimeagent.RuntimeVersion,
	}
}

func withAPISecurity(next http.Handler, token string) http.Handler {
	token = strings.TrimSpace(token)
	if token == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isPublicProbePath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		want := "Bearer " + token
		got := strings.TrimSpace(r.Header.Get("Authorization"))
		if subtle.ConstantTimeCompare([]byte(got), []byte(want)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="aifar-runtime"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isPublicProbePath(path string) bool {
	return path == "/healthz" || path == "/readyz"
}

func writeMetrics(w http.ResponseWriter, manager *runtimeagent.Manager, build buildInfo) {
	metrics := manager.Metrics()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	fmt.Fprintf(w, "# HELP aifar_runtime_info Build and runtime contract information.\n")
	fmt.Fprintf(w, "# TYPE aifar_runtime_info gauge\n")
	fmt.Fprintf(w, "aifar_runtime_info{version=\"%s\",commit=\"%s\",build_date=\"%s\",runtime_version=\"%s\"} 1\n",
		promLabelValue(build.Version),
		promLabelValue(build.Commit),
		promLabelValue(build.BuildDate),
		promLabelValue(metrics.RuntimeVersion),
	)
	fmt.Fprintf(w, "# HELP aifar_runtime_runtimes Number of rendered Runtime resources loaded.\n")
	fmt.Fprintf(w, "# TYPE aifar_runtime_runtimes gauge\n")
	fmt.Fprintf(w, "aifar_runtime_runtimes %d\n", metrics.RuntimeCount)
	fmt.Fprintf(w, "# HELP aifar_runtime_listeners Number of active Service and Ingress listeners.\n")
	fmt.Fprintf(w, "# TYPE aifar_runtime_listeners gauge\n")
	fmt.Fprintf(w, "aifar_runtime_listeners %d\n", metrics.ListenerCount)
	fmt.Fprintf(w, "# HELP aifar_runtime_desired_replicas Desired managed container replicas.\n")
	fmt.Fprintf(w, "# TYPE aifar_runtime_desired_replicas gauge\n")
	fmt.Fprintf(w, "aifar_runtime_desired_replicas %d\n", metrics.DesiredReplicas)
	fmt.Fprintf(w, "# HELP aifar_runtime_ready_replicas Ready managed container replicas.\n")
	fmt.Fprintf(w, "# TYPE aifar_runtime_ready_replicas gauge\n")
	fmt.Fprintf(w, "aifar_runtime_ready_replicas %d\n", metrics.ReadyReplicas)
	fmt.Fprintf(w, "# HELP aifar_runtime_failed_runtimes Number of Runtime resources in Failed phase.\n")
	fmt.Fprintf(w, "# TYPE aifar_runtime_failed_runtimes gauge\n")
	fmt.Fprintf(w, "aifar_runtime_failed_runtimes %d\n", metrics.FailedRuntimeCount)
	fmt.Fprintf(w, "# HELP aifar_runtime_endpoints Number of cached upstream endpoints.\n")
	fmt.Fprintf(w, "# TYPE aifar_runtime_endpoints gauge\n")
	fmt.Fprintf(w, "aifar_runtime_endpoints %d\n", metrics.EndpointCount)
}

func promLabelValue(value string) string {
	return strings.NewReplacer(`\`, `\\`, "\n", `\n`, `"`, `\"`).Replace(value)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeStdoutJSON(value any) {
	_ = json.NewEncoder(os.Stdout).Encode(value)
}
