// Package server is the micro server which runs the whole system
package server

import (
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/micro/micro/v3/cmd"
	"github.com/micro/micro/v3/service/auth"
	log "github.com/micro/micro/v3/service/logger"
	"github.com/micro/micro/v3/service/runtime"
	"github.com/urfave/cli/v2"
)

var (
	// list of services managed
	services = []string{
		"registry", // :8000
		"broker",   // :8003
		"network",  // :8443
		"runtime",  // :8088
		"config",   // :8001
		"store",    // :8002
		"events",   // :unset
		"auth",     // :8010
		"proxy",    // :8081
		"api",      // :8080
	}

	// list the clients managed
	clients = []string{
		"web",
	}
)

var (
	// Name of the server microservice
	Name = "server"
	// Address is the server address
	Address = ":10001"
)

func init() {
	command := &cli.Command{
		Name:  "server",
		Usage: "Run the micro server",
		Description: `Launching the micro server ('micro server') will enable one to connect to it by
		setting the appropriate Micro environment (see 'micro env' && 'micro env --help') commands.`,
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "address",
				Usage:   "Set the micro server address :10001",
				EnvVars: []string{"MICRO_SERVER_ADDRESS"},
			},
			&cli.StringFlag{
				Name:    "image",
				Usage:   "Set the micro server image",
				EnvVars: []string{"MICRO_SERVER_IMAGE"},
				Value:   "micro/micro:latest",
			},
		},
		Action: func(ctx *cli.Context) error {
			Run(ctx)
			return nil
		},
	}

	cmd.Register(command)
}

// Run runs the entire platform
func Run(context *cli.Context) error {
	if context.Args().Len() > 0 {
		cli.ShowSubcommandHelp(context)
		os.Exit(1)
	}

	// TODO: reimplement peering of servers e.g --peer=node1,node2,node3
	// peers are configured as network nodes to cluster between
	log.Info("Starting server")

	// parse the env vars
	var envvars []string
	for _, val := range os.Environ() {
		comps := strings.Split(val, "=")
		if len(comps) != 2 {
			continue
		}

		// only process MICRO_ values
		if !strings.HasPrefix(comps[0], "MICRO_") {
			continue
		}

		// skip the profile and proxy, that's set below since it can be service specific
		if comps[0] == "MICRO_PROFILE" || comps[0] == "MICRO_PROXY" {
			continue
		}

		envvars = append(envvars, val)
	}

	// save the runtime
	runtimeServer := runtime.DefaultRuntime

	// start the services
	for _, service := range services {
		log.Infof("Registering %s", service)

		// all things run by the server are `micro service [name]`
		cmdArgs := []string{"service"}

		// TODO: remove hacks
		profile := context.String("profile")

		env := envvars
		env = append(env, "MICRO_PROFILE="+profile)

		// set the proxy address, default to the network running locally
		if service != "network" {
			proxy := context.String("proxy_address")
			if len(proxy) == 0 {
				proxy = "127.0.0.1:8443"
			}
			env = append(env, "MICRO_PROXY="+proxy)
		}

		// for kubernetes we want to provide a port and instruct the service to bind to it. we don't do
		// this locally because the services are not isolated and the ports will conflict
		port := "8080"

		// we want to pass through the global args so go up one level in the context lineage
		if len(context.Lineage()) > 1 {
			globCtx := context.Lineage()[1]
			for _, f := range globCtx.FlagNames() {
				cmdArgs = append(cmdArgs, "--"+f, context.String(f))
			}
		}
		cmdArgs = append(cmdArgs, service)

		// runtime based on environment we run the service in
		args := []runtime.CreateOption{
			runtime.WithCommand(os.Args[0]),
			runtime.WithArgs(cmdArgs...),
			runtime.WithEnv(env),
			runtime.WithPort(port),
			runtime.WithRetries(10),
			runtime.WithSecret("MICRO_AUTH_PUBLIC_KEY", auth.DefaultAuth.Options().PublicKey),
			runtime.WithSecret("MICRO_AUTH_PRIVATE_KEY", auth.DefaultAuth.Options().PrivateKey),
		}

		// NOTE: we use Version right now to check for the latest release
		muService := &runtime.Service{Name: service, Version: "latest"}
		if err := runtimeServer.Create(muService, args...); err != nil {
			log.Errorf("Failed to create runtime environment: %v", err)
			return err
		}
	}

	// start the clients
	for _, client := range clients {
		log.Infof("Registering %s", client)

		// runtime based on environment we run the service in
		args := []runtime.CreateOption{
			runtime.WithCommand(os.Args[0]),
			runtime.WithArgs(client),
			runtime.WithRetries(10),
		}

		// NOTE: we use Version right now to check for the latest release
		srv := &runtime.Service{Name: client, Version: "latest"}
		if err := runtimeServer.Create(srv, args...); err != nil {
			log.Errorf("Failed to create runtime environment: %v", err)
			return err
		}
	}

	log.Info("Starting server runtime")

	// start the runtime
	if err := runtimeServer.Start(); err != nil {
		log.Fatal(err)
		return err
	}

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGKILL)
	<-ch

	runtimeServer.Stop()
	log.Info("Stopped server")

	// just wait 1 sec
	<-time.After(time.Second)

	return nil
}
