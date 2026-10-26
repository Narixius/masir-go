package main

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/narixius/masir-go/apps/masir/internal/ui"
	"github.com/narixius/masir-go/libs/masir-core/client"
	"github.com/narixius/masir-go/libs/masir-core/paths"
	"github.com/narixius/masir-go/libs/masir-core/service"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

var cachedClient pb.DaemonServiceClient

// liveSocket is where a daemon answers right now.
func liveSocket() (string, bool) { return client.LiveSocket() }

// daemonAlive reports whether any daemon answers.
func daemonAlive() bool { return client.Alive() }

// daemon returns a client, starting a background daemon first if none answers.
func daemon() (pb.DaemonServiceClient, error) {
	if cachedClient != nil {
		return cachedClient, nil
	}
	sock, err := paths.SocketPath() // where a daemon starts, if none answers
	if err != nil {
		return nil, err
	}
	if err := ensureDaemon(sock, true); err != nil { // idle: only `masir connect` starts Xray
		return nil, err
	}
	return dial()
}

// runningDaemon returns a client only if a daemon already answers; it never starts one.
func runningDaemon() (pb.DaemonServiceClient, bool) {
	if !daemonAlive() {
		return nil, false
	}
	c, err := dial()
	return c, err == nil
}

// dial connects to whichever daemon answers; see client.Dial.
func dial() (pb.DaemonServiceClient, error) {
	_, c, err := client.Dial() // the process ends soon after; nothing to close
	if err != nil {
		return nil, err
	}
	cachedClient = c
	return c, nil
}

// appCtx ends when the user quits: Ctrl-C, SIGTERM, or the terminal closing. Every
// daemon call and latency probe derives from it, so quitting cancels them all.
var appCtx = context.Background()

// rpc is a context for one daemon call. Fetching a subscription can take a while.
func rpc() (context.Context, context.CancelFunc) {
	return context.WithTimeout(appCtx, 90*time.Second)
}

// cancelled reports whether err means the user quit, rather than a failure.
func cancelled(err error) bool {
	if ui.IsCancel(err) || errors.Is(err, context.Canceled) {
		return true
	}
	return status.Code(err) == codes.Canceled || appCtx.Err() != nil
}

// rpcTimeoutShort bounds calls a person is waiting on interactively, like completions.
const rpcTimeoutShort = 3 * time.Second

// call runs one daemon call with a fresh context.
func call[T any](fn func(ctx context.Context, c pb.DaemonServiceClient) (T, error)) (T, error) {
	var zero T
	c, err := daemon()
	if err != nil {
		return zero, err
	}
	ctx, cancel := rpc()
	defer cancel()
	res, err := fn(ctx, c)
	if err == nil || inRulesPrompt || errorReason(err) != "RULES_BROKEN" {
		return res, err
	}
	// The daemon won't start or change Xray while rules can't be built. It has said what
	// is wrong; the user decides, and the call that was refused is tried once more.
	retry, problems, perr := brokenRulesFlow()
	if perr != nil {
		return zero, perr
	}
	if !retry {
		return zero, &userError{
			headline: brokenRulesHeadline(len(problems)),
			details:  problemText(problems),
			advice:   brokenRulesAdvice(problems),
			note:     "Nothing was changed.",
		}
	}
	ctx2, cancel2 := rpc()
	defer cancel2()
	return fn(ctx2, c)
}

// errorReason is the masir ErrorInfo reason on a status error, e.g. "ROOT_REQUIRED".
func errorReason(err error) string { return client.ErrorReason(err) }

// message turns an error into what the user reads: the daemon's message without the
// gRPC wrapping, or what to do when the daemon can't be reached.
func message(err error) string {
	if ue, ok := asUserError(err); ok {
		return ue.Error()
	}
	msg, adv := describe(err)
	if adv.empty() {
		return msg
	}
	return msg + "\n" + adv.plain()
}

// describe splits an error into what went wrong and what to do about it, so the UI can
// show them in separate places.
func describe(err error) (string, advice) {
	if ue, ok := asUserError(err); ok {
		return ue.headline, ue.advice
	}
	st, ok := status.FromError(err)
	if !ok {
		return err.Error(), advice{}
	}
	// The daemon itself answers Unavailable when a subscription provider can't be
	// reached; only a failed connection to the daemon means the daemon is gone.
	if st.Code() == codes.Unavailable && strings.Contains(st.Message(), "connection error") {
		return "Can't reach the masir daemon", advice{"Start it with:", []string{"masir daemon start"}}
	}
	if st.Code() == codes.Unimplemented {
		return "The running masir daemon is older than this masir", staleDaemonAdvice()
	}
	msg := st.Message()
	return msg, explain(msg)
}

// showError prints a failure the way the rest of masir looks.
func showError(err error) {
	if ue, ok := asUserError(err); ok {
		printFailure(ue.headline, ue.details, ue.advice, ue.note)
		return
	}
	msg, adv := describe(err)
	printFailure(msg, "", adv, "")
}

var portRe = regexp.MustCompile(`:(\d{2,5}): bind: address already in use`)

// explain turns errors that come from the system into what to do about them.
func explain(msg string) advice {
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "failed to add system route") && strings.Contains(lower, "file exists"):
		return advice{"Another VPN or TUN app already routes your traffic. Quit it, then run:", []string{"masir connect"}}
	case strings.Contains(lower, "address already in use"):
		if m := portRe.FindStringSubmatch(msg); m != nil {
			return advice{fmt.Sprintf("Port %s is taken by another program. Quit it, or move masir to another port, for example:", m[1]),
				[]string{"masir settings set mixedPort 7891"}}
		}
		return advice{text: "A port masir needs is taken by another program. Quit the other VPN or proxy, then try again."}
	case strings.Contains(lower, "operation not permitted") && strings.Contains(lower, "tun"):
		return advice{"TUN needs the daemon to run as root. Set that up once:", []string{"masir daemon enable"}}
	case strings.Contains(lower, "wintun.dll"):
		return advice{"TUN on Windows needs wintun.dll beside masir.exe; the service writes it there from masir.exe when it starts. " +
			"Reinstall the service, and look at the log if this persists:", []string{"masir daemon enable", "masir logs"}}
	case strings.Contains(lower, "failed to load code") || strings.Contains(lower, "failed to check code"):
		return advice{"That category isn't in the installed geo data (Loyalsoldier/v2ray-rules-dat): geoip.dat has every " +
			"two-letter country code plus lists such as private, google, telegram, netflix and cloudflare. Fetch the latest data:",
			[]string{"masir update geo"}}
	case strings.Contains(lower, "geosite") || strings.Contains(lower, "geoip.dat"):
		return advice{"geosite: and geoip: rules need geoip.dat and geosite.dat next to the masir executable. Download them:", []string{"masir update geo"}}
	case strings.Contains(lower, "permission denied"):
		return advice{"masir doesn't have permission to write there. If it's the system service or a root-owned file, do one of these once:",
			[]string{"masir daemon enable", "sudo masir update"}}
	case strings.Contains(lower, "read-only file system"):
		return advice{text: "That location is read-only. Move masir (and its geo files) somewhere you can write."}
	case strings.Contains(lower, "no space left on device"):
		return advice{text: "The disk is full. Free some space, then try again."}
	case strings.Contains(lower, "text file busy"):
		return advice{text: "The file is still running. Try again in a moment."}
	case strings.Contains(lower, "being used by another process"):
		return advice{text: "Another process is using the file. Close it, then try again."}
	case strings.Contains(lower, "no such host") || strings.Contains(lower, "name resolution"):
		return advice{"The name didn't resolve. Check your connection and DNS; this shows both:", []string{"masir status"}}
	case strings.Contains(lower, "connection refused"):
		return advice{"Nothing accepted the connection. Check the address and port, or switch proxy:", []string{"masir use"}}
	case strings.Contains(lower, "i/o timeout") || strings.Contains(lower, "deadline exceeded") || strings.Contains(lower, "timed out"):
		return advice{"The request timed out. Check your connection, or try another proxy:", []string{"masir use --fastest"}}
	case strings.Contains(lower, "certificate") || strings.Contains(lower, "tls handshake"):
		return advice{text: "TLS failed. The network may be filtered, or the server's certificate is wrong."}
	case strings.Contains(lower, "401") || strings.Contains(lower, "403") || strings.Contains(lower, "404") || strings.Contains(lower, "429"):
		return advice{"The provider rejected the request. The subscription may have expired or be rate-limited. Try refreshing it:", []string{"masir sub update"}}
	}
	return advice{}
}

// errReported means the command already told the user what went wrong.
var errReported = errors.New("reported")

func fail(format string, a ...any) error { return fmt.Errorf(format, a...) }

// staleDaemonAdvice says how to run a daemon that matches this masir: the root service
// keeps its own copy of masir, which only `daemon enable` or `update` replaces.
func staleDaemonAdvice() advice {
	if service.Installed() {
		return advice{"Install the new one into the service:", []string{"masir daemon enable"}}
	}
	return advice{"Restart it to run this version:", []string{"masir daemon restart"}}
}
