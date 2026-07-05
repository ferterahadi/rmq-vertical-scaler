// Command rmq-vertical-scaler is the v2 (Go) entry point. It exposes two
// subcommands: `generate` (the local manifest generator, port of
// bin/rmq-vertical-scaler) and `run` (the in-cluster control loop, the default
// container command).
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/ferterahadi/rmq-vertical-scaler/internal/config"
	"github.com/ferterahadi/rmq-vertical-scaler/internal/controller"
	"github.com/ferterahadi/rmq-vertical-scaler/internal/k8s"
	"github.com/ferterahadi/rmq-vertical-scaler/internal/manifests"
	"github.com/ferterahadi/rmq-vertical-scaler/internal/metrics"
	"github.com/ferterahadi/rmq-vertical-scaler/schema"

	"github.com/spf13/cobra"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := rootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}

// rootCmd builds the command tree. Kept separate from main() so the commands
// are testable without invoking os.Exit.
func rootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:     "rmq-vertical-scaler",
		Short:   "Automatically scale RabbitMQ cluster CPU/memory based on queue metrics",
		Version: version,
		Long: "Generate Kubernetes manifests for, or run, the RabbitMQ Vertical Scaler.\n\n" +
			"Common usage:\n" +
			"  rmq-vertical-scaler generate --namespace prod --service-name my-rabbitmq\n" +
			"  rmq-vertical-scaler run   # in-cluster control loop (default container command)",
		SilenceUsage: true,
	}
	root.AddCommand(generateCmd(), runCmd())
	return root
}

func generateCmd() *cobra.Command {
	var configPath string
	var f manifests.Flags

	cmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate Kubernetes deployment manifests for RabbitMQ Vertical Scaler",
		RunE: func(cmd *cobra.Command, _ []string) error {
			out := cmd.OutOrStdout()
			var configJSON []byte
			if configPath != "" {
				b, err := os.ReadFile(configPath)
				if err != nil {
					return fmt.Errorf("failed to load config file: %w", err)
				}
				configJSON = b
				if err := schema.Validate(b); err != nil {
					return fmt.Errorf("invalid config %s: %w", configPath, err)
				}
				fmt.Fprintf(out, "📄 Loaded configuration from: %s\n", configPath)
			}

			yaml, summary, err := manifests.Generate(f, configJSON)
			if err != nil {
				return fmt.Errorf("failed to generate manifests: %w", err)
			}
			if err := os.WriteFile(summary.Output, []byte(yaml), 0o644); err != nil {
				return fmt.Errorf("failed to write %s: %w", summary.Output, err)
			}
			printSummary(out, summary)
			return nil
		},
	}

	cmd.Flags().StringVarP(&configPath, "config", "c", "", "Path to JSON configuration file (see examples/ for templates)")
	cmd.Flags().StringVarP(&f.Namespace, "namespace", "n", "", "Kubernetes namespace where RabbitMQ and scaler will be deployed")
	cmd.Flags().StringVarP(&f.ServiceName, "service-name", "s", "", "Name of the RabbitMQ service/cluster (used for DNS and secrets)")
	cmd.Flags().StringVarP(&f.Output, "output", "o", "", "Output YAML file name for the generated manifests")
	cmd.Flags().StringVar(&f.Image, "image", "", "Docker image to use for the scaler deployment")
	cmd.Flags().StringVar(&f.ScalerName, "scaler-name", "", "Custom name for scaler resources (ServiceAccount, Role, etc.)")
	return cmd
}

func runCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "run",
		Short: "Run the in-cluster vertical scaling controller",
		RunE: func(_ *cobra.Command, _ []string) error {
			logger := log.New(os.Stdout, "", 0)
			logger.Println("🐰 RabbitMQ Vertical Scaler")
			logger.Println("==============================")

			cfg := config.Load()
			collector := metrics.New(cfg.RMQHost, cfg.RMQPort, cfg.RMQUser, cfg.RMQPass, logger)
			kube, err := k8s.New(cfg, logger)
			if err != nil {
				return fmt.Errorf("init kubernetes client: %w", err)
			}

			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			return controller.New(cfg, collector, kube, logger).Run(ctx)
		},
	}
}

func printSummary(out io.Writer, s manifests.Summary) {
	fmt.Fprintln(out, "🔧 Generating Kubernetes manifests...")
	fmt.Fprintf(out, "📝 Namespace: %s\n", s.Namespace)
	fmt.Fprintf(out, "🐰 RabbitMQ Service: %s\n", s.ServiceName)
	fmt.Fprintf(out, "📦 Image: %s\n", s.Image)
	fmt.Fprintf(out, "🏷️  Scaler Name: %s\n", s.ScalerName)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "📊 Scaling Profiles:")
	for _, p := range s.Profiles {
		if p.Name == "LOW" {
			fmt.Fprintf(out, "  %s:      CPU=%s, Memory=%s\n", p.Name, p.CPU, p.Memory)
			continue
		}
		q := orNA(s.QueueThresholds[p.Name])
		r := orNA(s.RateThresholds[p.Name])
		fmt.Fprintf(out, "  %s:   CPU=%s, Memory=%s (triggers at: %s queue depth, %s msg/s)\n",
			p.Name, p.CPU, p.Memory, q, r)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "⏱️  Timing Configuration:")
	fmt.Fprintf(out, "  Scale Up Debounce: %ss\n", s.ScaleUpSeconds)
	fmt.Fprintf(out, "  Scale Down Debounce: %ss\n", s.ScaleDnSeconds)
	fmt.Fprintf(out, "  Check Interval: %ss\n", s.CheckInterval)
	fmt.Fprintln(out)
	fmt.Fprintf(out, "✅ Generated %s\n", s.Output)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "📋 Next steps:")
	fmt.Fprintln(out, "  1. Review the generated configuration")
	fmt.Fprintf(out, "  2. Apply to your cluster: kubectl apply -f %s\n", s.Output)
	fmt.Fprintf(out, "  3. Monitor the deployment: kubectl logs -f deployment/%s -n %s\n", s.ScalerName, s.Namespace)
}

func orNA(v string) string {
	if v == "" {
		return "N/A"
	}
	return v
}
