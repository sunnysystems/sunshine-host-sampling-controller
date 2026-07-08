// Command controller is the Sunshine host-sampling controller.
//
// It runs in a customer's Kubernetes cluster, polls its host-sampling policy
// from Sunshine (authenticated by a scoped inbound token), and reconciles
// the sampled-out node label toward the plan. Actuation is triple-locked: the
// local DRY_RUN env selects DryRun vs LabelActuator, the server downgrades the
// served policy to dry_run unless the org's execute flag is on (and it is not a
// demo org), and the LabelActuator writes labels only when the served mode is
// "active". DRY_RUN defaults to true, so the controller never mutates a cluster
// unless explicitly configured and authorised by the server.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/sunnysystems/sunshine-host-sampling-controller/internal/actuator"
	"github.com/sunnysystems/sunshine-host-sampling-controller/internal/config"
	"github.com/sunnysystems/sunshine-host-sampling-controller/internal/kube"
	"github.com/sunnysystems/sunshine-host-sampling-controller/internal/metrics"
	"github.com/sunnysystems/sunshine-host-sampling-controller/internal/policy"
	"github.com/sunnysystems/sunshine-host-sampling-controller/internal/reconcile"
	"github.com/sunnysystems/sunshine-host-sampling-controller/internal/report"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	cfg, err := config.FromEnv()
	if err != nil {
		log.Error("configuration error", "err", err)
		os.Exit(1)
	}
	restCfg, err := rest.InClusterConfig()
	if err != nil {
		log.Error("in-cluster config error (this controller runs inside the cluster)", "err", err)
		os.Exit(1)
	}
	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		log.Error("kubernetes client error", "err", err)
		os.Exit(1)
	}

	// Local kill-switch (defense in depth): DRY_RUN=true (the default) selects
	// the reporting-only actuator. Even with DRY_RUN=false the LabelActuator
	// still only writes labels when the server serves mode "active".
	var act actuator.Actuator = actuator.DryRun{Log: log}
	if !cfg.DryRun {
		act = actuator.LabelActuator{Labeler: kube.NewLabeler(clientset), Log: log}
		log.Warn("execute mode enabled — controller may write sampled-out labels when the served policy is active")
	}

	reg := metrics.New()

	// Enforcement preflight: labelling a node only removes its agent if the
	// agent DaemonSet has the inverted nodeAffinity on the sampled-out label.
	// Verify it (read-only) so a misconfigured install surfaces before it
	// produces phantom savings. Optional — skipped when the DaemonSet is unset.
	if cfg.AgentDaemonSetNamespace != "" && cfg.AgentDaemonSetName != "" {
		pfCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		present, err := kube.NewAffinityChecker(clientset).HasSampledOutAntiAffinity(
			pfCtx, cfg.AgentDaemonSetNamespace, cfg.AgentDaemonSetName, actuator.LabelSampledOut)
		cancel()
		switch {
		case err != nil:
			log.Warn("enforcement preflight: could not read the agent DaemonSet — cannot confirm labels will take effect",
				"namespace", cfg.AgentDaemonSetNamespace, "name", cfg.AgentDaemonSetName, "err", err)
		case present:
			reg.SetEnforcementAffinity(true)
			log.Info("enforcement preflight: agent DaemonSet carries the sampled-out anti-affinity — labels will take effect",
				"namespace", cfg.AgentDaemonSetNamespace, "name", cfg.AgentDaemonSetName)
		default:
			reg.SetEnforcementAffinity(false)
			log.Warn("enforcement preflight: agent DaemonSet is MISSING the sampled-out anti-affinity — sampling a node will NOT remove its agent (no savings); add the nodeAffinity (see chart README) before enabling execute",
				"namespace", cfg.AgentDaemonSetNamespace, "name", cfg.AgentDaemonSetName)
		}
	}

	reconciler := &reconcile.Reconciler{
		Policy:         policy.NewClient(cfg.Endpoint, cfg.Token, 10*time.Second),
		Nodes:          kube.NewLister(clientset),
		Actuator:       act,
		Metrics:        reg,
		Log:            log,
		Reporter:       report.NewClient(cfg.Endpoint, cfg.Token, 10*time.Second, log),
		ExecuteEnabled: !cfg.DryRun,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", reg.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	srv := &http.Server{Addr: cfg.MetricsAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("metrics server failed", "err", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Info("host-sampling-controller started",
		"dryRun", cfg.DryRun,
		"endpoint", cfg.Endpoint,
		"cluster", cfg.ClusterID,
		"pollInterval", cfg.PollInterval.String(),
	)
	reconciler.Run(ctx, cfg.PollInterval)

	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
	log.Info("host-sampling-controller stopped")
}
