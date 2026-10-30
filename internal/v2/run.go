package v2

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/ferterahadi/rmq-vertical-scaler/v2/internal/policy"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func Run(ctx context.Context, p *policy.Config) error {
	if err := policy.RejectLegacyEnv(); err != nil {
		return err
	}
	if p.NeedsRabbitMQ() && (os.Getenv("RMQ_USER") == "" || os.Getenv("RMQ_PASS") == "") {
		return fmt.Errorf("RabbitMQ triggers require Secret-backed RMQ_USER and RMQ_PASS")
	}
	name := os.Getenv("STATUS_CONFIG_MAP")
	if name == "" {
		name = "rmq-vertical-scaler-status"
	}
	if len(validation.IsDNS1123Subdomain(name)) > 0 {
		return fmt.Errorf("invalid STATUS_CONFIG_MAP")
	}
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("version-2 in-cluster client: %w", err)
	}
	cfg.Timeout = 10 * time.Second
	core, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return err
	}
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	b := &Backend{Policy: p, Core: core, Dynamic: dyn, User: os.Getenv("RMQ_USER"), Password: os.Getenv("RMQ_PASS")}
	return NewController(b, name, logger, time.Now()).Run(ctx)
}
