package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/cwithw/ScrcpyCat/adb-deployer/internal/deployer"
)

func parseConfig(args []string, getenv func(string) string) (deployer.Config, error) {
	config := deployer.Config{}
	flag := flag.NewFlagSet("adb-deployer", flag.ContinueOnError)
	mode := getenv("SCRCPYCAT_ADB_MODE")
	if mode == "" {
		mode = deployer.ModeHost
	}
	flag.StringVar(&config.Mode, "mode", mode, "host (Linux Agent) or device (standalone Android Agent)")
	flag.StringVar(&config.ADBServer, "adb-server", "127.0.0.1:5037", "existing ADB server address")
	flag.StringVar(&config.HostAgent, "host-agent", "/usr/local/bin/scrcpycat-agent", "Linux Agent executable")
	flag.StringVar(&config.StateDir, "state-dir", "/var/lib/scrcpycat/adb-deployer", "persistent per-device state directory")
	flag.StringVar(&config.ADBPath, "adb", "adb", "adb executable")
	flag.StringVar(&config.ArtifactDir, "artifact-dir", "/opt/scrcpycat/agent/bin", "agent ABI artifact directory")
	flag.StringVar(&config.ScrcpyJar, "scrcpy-jar", "/opt/scrcpycat/scrcpy/scrcpy-server", "scrcpy server jar")
	flag.StringVar(&config.SignalingURL, "signaling", "", "agent register_agent WSS URL")
	flag.StringVar(&config.ControlPlaneURL, "control-plane", "", "control-plane HTTPS URL used to issue enrollment tokens")
	flag.StringVar(&config.DeploymentToken, "deployment-token", "", "scoped credential used to issue enrollment tokens")
	flag.StringVar(&config.AdminToken, "admin-token", "", "legacy admin Bearer token used to issue enrollment tokens")
	flag.StringVar(&config.EnrollmentToken, "enrollment-token", "", "fallback fixed enrollment token for one device")
	flag.DurationVar(&config.Interval, "interval", 10_000_000_000, "device scan interval")
	flag.BoolVar(&config.InsecureTLS, "insecure-tls", false, "allow a self-signed control-plane TLS certificate")
	flag.BoolVar(&config.Force, "force", false, "replace a healthy Android Agent on every scan (device mode only)")
	if err := flag.Parse(args); err != nil {
		return config, err
	}
	config.ResolveDefaults()
	if config.DeploymentToken == "" {
		config.DeploymentToken = getenv("SCRCPYCAT_DEPLOYMENT_TOKEN")
	}
	if config.AdminToken == "" {
		config.AdminToken = getenv("SCRCPYCAT_ADMIN_TOKEN")
	}
	return config, nil
}

func main() {
	config, err := parseConfig(os.Args[1:], os.Getenv)
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if err != nil {
		log.Fatal(err)
	}
	if err := config.Validate(); err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := deployer.Run(ctx, config); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}
