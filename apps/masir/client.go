package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
	"time"

	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/narixius/masir-go/apps/masir/internal/ui"
	"github.com/narixius/masir-go/libs/masir-core/paths"
	"github.com/narixius/masir-go/libs/masir-core/service"
	"github.com/narixius/masir-go/libs/masir-core/transport"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

var cachedClient pb.DaemonServiceClient

// liveSocket is where a daemon answers right now: a root daemon's socket if there is
// one, otherwise the socket of a daemon running as this user. Either way the CLI needs
// no privileges to reach it.
func liveSocket() (string, bool) {
	socks, err := paths.Sockets()
	if err != nil {
		return "", false
	}
	for _, s := range socks {
		if transport.Alive(s) {
			return s, true
		}
	}
	return "", false
}

// daemonAlive reports whether any daemon answers.
func daemonAlive() bool {
	_, ok := liveSocket()
	return ok
}

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

// dial connects to whichever daemon answers. The socket is looked up again on every
// (re)connect: a root daemon taking over from this user's daemon listens somewhere else.
func dial() (pb.DaemonServiceClient, error) {
	userSock, err := paths.SocketPath()
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient("passthrough:///masir-daemon",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			sock, ok := liveSocket()
			if !ok {
				sock = userSock
			}
			return transport.DialContext(ctx, sock)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	cachedClient = pb.NewDaemonServiceClient(conn) // the process ends soon after; nothing to close
	return cachedClient, nil
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
	retry, perr := brokenRulesFlow()
	if perr != nil {
		return zero, perr
	}
	if !retry {
		return zero, fail("%s. %s", strings.TrimSuffix(message(err), "."), brokenRulesHint)
	}
	ctx2, cancel2 := rpc()
	defer cancel2()
	return fn(ctx2, c)
}

// errorReason is the masir ErrorInfo reason on a status error, e.g. "ROOT_REQUIRED".
func errorReason(err error) string {
	for _, d := range status.Convert(err).Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok && info.Domain == "masir" {
			return info.Reason
		}
	}
	return ""
}

// message turns an error into what the user reads: the daemon's message without the
// gRPC wrapping, or what to do when the daemon can't be reached.
func message(err error) string {
	msg, hint := describe(err)
	if hint != "" {
		return msg + "\n" + hint
	}
	return msg
}

// describe splits an error into what went wrong and the next step, so the UI can style
// them separately.
func describe(err error) (string, string) {
	st, ok := status.FromError(err)
	if !ok {
		return err.Error(), ""
	}
	// The daemon itself answers Unavailable when a subscription provider can't be
	// reached; only a failed connection to the daemon means the daemon is gone.
	if st.Code() == codes.Unavailable && strings.Contains(st.Message(), "connection error") {
		return "Can't reach the masir daemon", "start it with `masir daemon start`"
	}
	if st.Code() == codes.Unimplemented {
		return "The running masir daemon is older than this masir", staleDaemonHint()
	}
	msg := st.Message()
	return msg, explain(msg)
}

// showError prints a failure the way the rest of masir looks.
func showError(err error) {
	msg, hint := describe(err)
	ui.Problem(msg, hint)
}

var portRe = regexp.MustCompile(`:(\d{2,5}): bind: address already in use`)

// explain turns errors that come from the system into what to do about them.
func explain(msg string) string {
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "failed to add system route") && strings.Contains(lower, "file exists"):
		return "Another VPN or TUN app already routes your traffic. Quit it, then run `masir connect`."
	case strings.Contains(lower, "address already in use"):
		if m := portRe.FindStringSubmatch(msg); m != nil {
			return fmt.Sprintf("Port %s is taken by another program. Quit it, or move masir with `masir settings set mixedPort <port>`.", m[1])
		}
		return "A port masir needs is taken by another program. Quit the other VPN or proxy, then try again."
	case strings.Contains(lower, "operation not permitted") && strings.Contains(lower, "tun"):
		return "TUN needs the daemon to run as root: `masir daemon enable` sets that up."
	case strings.Contains(lower, "wintun.dll"):
		return "TUN on Windows needs wintun.dll beside masir.exe; the service writes it there from masir.exe " +
			"when it starts. Reinstall the service with `masir daemon enable` and see `masir logs` if this persists."
	case strings.Contains(lower, "failed to load code") || strings.Contains(lower, "failed to check code"):
		return "That category isn't in the installed geo data (Loyalsoldier/v2ray-rules-dat): geoip.dat has every " +
			"two-letter country code plus lists such as private, google, telegram, netflix and cloudflare; " +
			"`masir update geo` fetches the latest."
	case strings.Contains(lower, "geosite") || strings.Contains(lower, "geoip.dat"):
		return "geosite:/geoip: rules need geoip.dat and geosite.dat next to the masir executable: `masir update geo` downloads them."
	case strings.Contains(lower, "permission denied"):
		return "masir doesn't have permission to write there. If it's the system service or a root-owned file, run `masir daemon enable` or `sudo masir update` once."
	case strings.Contains(lower, "read-only file system"):
		return "That location is read-only. Move masir (and its geo files) somewhere you can write."
	case strings.Contains(lower, "no space left on device"):
		return "The disk is full. Free some space, then try again."
	case strings.Contains(lower, "text file busy"):
		return "The file is still running. Try again in a moment."
	case strings.Contains(lower, "being used by another process"):
		return "Another process is using the file. Close it, then try again."
	case strings.Contains(lower, "no such host") || strings.Contains(lower, "name resolution"):
		return "The name didn't resolve. Check your connection or DNS; `masir status` shows both."
	case strings.Contains(lower, "connection refused"):
		return "Nothing accepted the connection. Check the address and port, or switch proxy with `masir use`."
	case strings.Contains(lower, "i/o timeout") || strings.Contains(lower, "deadline exceeded") || strings.Contains(lower, "timed out"):
		return "The request timed out. Check your connection, or try another proxy with `masir use --fastest`."
	case strings.Contains(lower, "certificate") || strings.Contains(lower, "tls handshake"):
		return "TLS failed. The network may be filtered, or the server's certificate is wrong."
	case strings.Contains(lower, "401") || strings.Contains(lower, "403") || strings.Contains(lower, "404") || strings.Contains(lower, "429"):
		return "The provider rejected the request. The subscription may have expired or be rate-limited; try `masir sub update`."
	}
	return ""
}

// errReported means the command already told the user what went wrong.
var errReported = errors.New("reported")

func fail(format string, a ...any) error { return fmt.Errorf(format, a...) }

// staleDaemonHint says how to run a daemon that matches this masir: the root service
// keeps its own copy of masir, which only `daemon enable` or `update` replaces.
func staleDaemonHint() string {
	if service.Installed() {
		return "Install the new one into the service with `masir daemon enable`, or use `masir update` once a release is out."
	}
	return "Restart it with `masir daemon restart` to run this version."
}
