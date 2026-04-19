package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	compute "google.golang.org/api/compute/v1"
)

// sentinel used to break out of Pages iteration early.
var errInstanceFound = errors.New("instance found")

// ─── entry point ─────────────────────────────────────────────────────────────

func main() {
	args := os.Args
	program := filepath.Base(args[0])
	if program == "" {
		program = "gvm"
	}

	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <start|status|ssh|tmux> [args...]\n", program)
		os.Exit(1)
	}

	subcommand := args[1]
	rest := args[2:]

	// GVM_INSTANCE is required for every subcommand.
	instanceName := mustEnv("GVM_INSTANCE",
		"\x1b[31mError:\x1b[0m GVM_INSTANCE environment variable is not set.")

	projectID := mustEnv("GOOGLE_CLOUD_PROJECT",
		"\x1b[31mError:\x1b[0m GOOGLE_CLOUD_PROJECT environment variable is not set.")

	if !credentialsAvailable() {
		fmt.Fprint(os.Stderr,
			"\x1b[31mError:\x1b[0m No Google Cloud credentials found.\n"+
				"Set the GOOGLE_APPLICATION_CREDENTIALS environment variable to point to a "+
				"service account key file, or run `gcloud auth application-default login` "+
				"to create application default credentials.\n")
		os.Exit(1)
	} else if runtime.GOOS == "windows" {
		// Ensure the SDK can locate application default credentials on Windows.
		if appData := os.Getenv("LOCALAPPDATA"); appData != "" {
			credPath := filepath.Join(appData, "gcloud", "application_default_credentials.json")
			os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", credPath)
		}
	}

	ctx := context.Background()

	svc, err := compute.NewService(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create compute client: %v\n", err)
		os.Exit(1)
	}

	// Look the instance up in the aggregated list to confirm it exists and to
	// discover its zone and current state.
	zone, status, ip, err := findInstance(ctx, svc, projectID, instanceName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error looking up instance: %v\n", err)
		os.Exit(1)
	}
	if zone == "" {
		fmt.Fprintf(os.Stderr,
			"instance `%s` not found in project `%s`\n", instanceName, projectID)
		os.Exit(1)
	}

	switch subcommand {

	// ── status ────────────────────────────────────────────────────────────────
	case "status":
		if status == "RUNNING" {
			fmt.Println("RUNNING")
		} else {
			fmt.Println("STOPPED")
		}

	// ── ssh ───────────────────────────────────────────────────────────────────
	case "ssh":
		user := requireGVMUser()
		if ip == "" {
			fmt.Fprintf(os.Stderr,
				"instance `%s` has no reachable IP address\n", instanceName)
			os.Exit(1)
		}

		fmt.Fprintf(os.Stderr,
			"\x1b[32mConnecting\x1b[0m to %s@%s (instance `%s` in zone %s)\n",
			user, ip, instanceName, zone)

		runSSH(append([]string{user + "@" + ip}, rest...))

	// ── tmux ──────────────────────────────────────────────────────────────────
	case "tmux":
		user := requireGVMUser()

		if len(rest) == 0 {
			fmt.Fprintf(os.Stderr,
				"\x1b[31mError:\x1b[0m Usage: %s tmux <session-name>\n", program)
			os.Exit(1)
		}
		sessionName := rest[0]

		if status != "RUNNING" {
			fmt.Fprintf(os.Stderr,
				"instance `%s` is not running (status: %s). "+
					"Use `%s start` to start it first.\n",
				instanceName, status, program)
			os.Exit(1)
		}
		if ip == "" {
			fmt.Fprintf(os.Stderr,
				"instance `%s` has no reachable IP address\n", instanceName)
			os.Exit(1)
		}

		fmt.Fprintf(os.Stderr,
			"\x1b[32mConnecting\x1b[0m to %s@%s "+
				"(instance `%s` in zone %s, tmux session `%s`)\n",
			user, ip, instanceName, zone, sessionName)

		// -t allocates a pseudo-TTY, which tmux requires.
		runSSH([]string{"-t", user + "@" + ip, "tmux new -As " + sessionName})

	// ── start ─────────────────────────────────────────────────────────────────
	case "start":
		// Require GVM_USER up front so we don't wait for a start just to
		// discover we can't complete the SSH step.
		user := requireGVMUser()

		if status == "RUNNING" {
			fmt.Fprintf(os.Stderr, "Instance `%s` is already RUNNING.\n", instanceName)
		} else {
			fmt.Fprintf(os.Stderr,
				"\x1b[32mStarting\x1b[0m instance `%s` in zone %s "+
					"(current status: %s)...\n",
				instanceName, zone, status)

			if _, err = svc.Instances.
				Start(projectID, zone, instanceName).
				Context(ctx).Do(); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to start instance: %v\n", err)
				os.Exit(1)
			}
		}

		timeoutSecs := uint64(180)
		if ts := os.Getenv("GVM_START_TIMEOUT"); ts != "" {
			if parsed, parseErr := strconv.ParseUint(ts, 10, 64); parseErr == nil {
				timeoutSecs = parsed
			}
		}

		readyIP, err := waitForSSH(
			ctx, svc, projectID, zone, instanceName,
			time.Duration(timeoutSecs)*time.Second,
		)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error waiting for SSH: %v\n", err)
			os.Exit(1)
		}

		fmt.Fprintf(os.Stderr,
			"\x1b[32mConnecting\x1b[0m to %s@%s (instance `%s` in zone %s)\n",
			user, readyIP, instanceName, zone)

		runSSH(append([]string{user + "@" + readyIP}, rest...))

	// ── unknown ───────────────────────────────────────────────────────────────
	default:
		fmt.Fprintf(os.Stderr,
			"Unknown subcommand `%s`. Usage: %s <start|status|ssh|tmux> [args...]\n",
			subcommand, program)
		os.Exit(1)
	}
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// mustEnv returns the value of the named environment variable, or prints msg
// and exits if it is unset/empty.
func mustEnv(name, msg string) string {
	v := os.Getenv(name)
	if v == "" {
		fmt.Fprintln(os.Stderr, msg)
		os.Exit(1)
	}
	return v
}

// requireGVMUser returns GVM_USER or exits with a clear error message.
func requireGVMUser() string {
	return mustEnv("GVM_USER",
		"\x1b[31mError:\x1b[0m GVM_USER environment variable is not set.")
}

// runSSH execs ssh with the given arguments, forwarding stdio, then exits with
// ssh's own exit code.  This function never returns normally.
func runSSH(args []string) {
	cmd := exec.Command("ssh", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.ExitCode())
		}
		os.Exit(1)
	}
	os.Exit(0)
}

// ─── GCE helpers ─────────────────────────────────────────────────────────────

// instanceResult holds the fields we care about from an Instance resource.
type instanceResult struct {
	zone, status, ip string
}

// findInstance scans the aggregated instance list for instanceName.
// Returns (zone, status, ip).  zone is stripped of its "zones/" prefix.
// All three strings are empty when the instance is not found.
func findInstance(
	ctx context.Context,
	svc *compute.Service,
	projectID, instanceName string,
) (zone, status, ip string, err error) {

	var result instanceResult

	pageErr := svc.Instances.AggregatedList(projectID).
		Pages(ctx, func(page *compute.InstanceAggregatedList) error {
			for zoneKey, sl := range page.Items {
				for _, inst := range sl.Instances {
					if inst.Name != instanceName {
						continue
					}

					result.zone = strings.TrimPrefix(zoneKey, "zones/")
					result.status = inst.Status

					// Prefer external (NAT) IP, fall back to internal IP.
				outer:
					for _, ni := range inst.NetworkInterfaces {
						for _, ac := range ni.AccessConfigs {
							if ac.NatIP != "" {
								result.ip = ac.NatIP
								break outer
							}
						}
					}
					if result.ip == "" && len(inst.NetworkInterfaces) > 0 {
						result.ip = inst.NetworkInterfaces[0].NetworkIP
					}

					// Stop pagination immediately.
					return errInstanceFound
				}
			}
			return nil
		})

	if pageErr != nil && !errors.Is(pageErr, errInstanceFound) {
		err = pageErr
		return
	}

	zone, status, ip = result.zone, result.status, result.ip
	return
}

// waitForSSH polls the instance until it is RUNNING and port 22 is reachable,
// or until timeout elapses.
func waitForSSH(
	ctx context.Context,
	svc *compute.Service,
	projectID, zone, instanceName string,
	timeout time.Duration,
) (string, error) {

	deadline := time.Now().Add(timeout)
	lastStatus := ""
	lastIP := ""

	for {
		if time.Now().After(deadline) {
			return "", fmt.Errorf(
				"timed out after %ds waiting for `%s` to accept SSH",
				int(timeout.Seconds()), instanceName,
			)
		}

		inst, err := svc.Instances.
			Get(projectID, zone, instanceName).
			Context(ctx).Do()
		if err != nil {
			return "", fmt.Errorf("polling instance: %w", err)
		}

		if inst.Status != lastStatus {
			fmt.Fprintf(os.Stderr, "  instance status: %s\n", inst.Status)
			lastStatus = inst.Status
		}

		// Extract IP (prefer NAT / external).
		var ip string
	outer:
		for _, ni := range inst.NetworkInterfaces {
			for _, ac := range ni.AccessConfigs {
				if ac.NatIP != "" {
					ip = ac.NatIP
					break outer
				}
			}
		}
		if ip == "" && len(inst.NetworkInterfaces) > 0 {
			ip = inst.NetworkInterfaces[0].NetworkIP
		}

		if inst.Status == "RUNNING" && ip != "" {
			if ip != lastIP {
				fmt.Fprintf(os.Stderr, "  probing SSH on %s:22 ...\n", ip)
				lastIP = ip
			}

			conn, dialErr := net.DialTimeout("tcp", ip+":22", 3*time.Second)
			if dialErr == nil {
				conn.Close()
				fmt.Fprintf(os.Stderr, "\x1b[32mSSH is ready\x1b[0m on %s\n", ip)
				return ip, nil
			}
		}

		time.Sleep(3 * time.Second)
	}
}

// ─── credential helpers ───────────────────────────────────────────────────────

func credentialsAvailable() bool {
	if path := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); path != "" {
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
			return true
		}
		fmt.Fprintf(os.Stderr,
			"\x1b[33mWarning:\x1b[0m GOOGLE_APPLICATION_CREDENTIALS is set to `%s`, "+
				"but that file does not exist.\n", path)
	}

	if adcPath := applicationDefaultCredentialsPath(); adcPath != "" {
		if fi, err := os.Stat(adcPath); err == nil && !fi.IsDir() {
			return true
		}
	}

	return false
}

func applicationDefaultCredentialsPath() string {
	switch runtime.GOOS {
	case "windows":
		appData := os.Getenv("LOCALAPPDATA")
		if appData == "" {
			return ""
		}
		return filepath.Join(appData, "gcloud", "application_default_credentials.json")
	default:
		home := os.Getenv("HOME")
		if home == "" {
			return ""
		}
		return filepath.Join(home, ".config", "gcloud", "application_default_credentials.json")
	}
}
