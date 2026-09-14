package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/cwithw/ScrcpyCat/agent/internal/agent"
)

func main() {
	config := agent.Config{}
	flag.StringVar(&config.ADBSerial, "adb-serial", "", "Android serial served through an existing ADB server")
	flag.StringVar(&config.ADBServer, "adb-server", "127.0.0.1:5037", "ADB server address (host mode)")
	flag.StringVar(&config.DeviceID, "device-id", "", "stable device identifier")
	flag.StringVar(&config.SignalingURL, "signaling", "", "wss:// control-plane/register_agent URL")
	flag.StringVar(&config.EnrollmentToken, "enrollment-token", "", "one-time enrollment token")
	flag.StringVar(&config.IdentityPath, "identity-file", "", "persistent identity file (defaults depend on mode)")
	flag.StringVar(&config.PIDPath, "pid-file", "", "single-instance PID file (defaults depend on mode)")
	flag.StringVar(&config.ScrcpyJar, "scrcpy-jar", "", "scrcpy JAR on the machine running this Agent")
	flag.StringVar(&config.ScrcpyVersion, "scrcpy-version", "4.1", "pinned scrcpy server version")
	flag.BoolVar(&config.HealthCheck, "health", false, "verify an already-running agent and exit")
	flag.Parse()
	config.ResolveDefaults()
	if config.EnrollmentToken == "" {
		config.EnrollmentToken = os.Getenv("SCRCPYCAT_ENROLLMENT_TOKEN")
	}

	if config.HealthCheck {
		if agent.Healthy(config.IdentityPath, config.PIDPath, config.ScrcpyJar) {
			os.Exit(0)
		}
		os.Exit(1)
	}
	if err := config.Validate(); err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := agent.Run(ctx, config); err != nil && !errors.Is(err, context.Canceled) {
		if errors.Is(err, agent.ErrAuthentication) {
			log.Print(err)
			os.Exit(3)
		}
		log.Fatal(err)
	}
}
