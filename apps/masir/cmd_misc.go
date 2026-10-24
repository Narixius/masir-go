package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/narixius/masir-go/apps/masir/internal/ui"
	pb "github.com/narixius/masir-go/libs/masir-proto/gen/masir/daemon/v1"
)

// cmdLogs follows the daemon's log until Ctrl-C.
func cmdLogs(args) error {
	c, err := daemon()
	if err != nil {
		return err
	}
	ctx := appCtx // Ctrl-C is how you stop following, so it ends quietly
	stream, err := c.StreamLogs(ctx, &pb.StreamLogsRequest{Follow: true})
	if err != nil {
		return err
	}
	if ui.Interactive() {
		fmt.Fprintln(os.Stderr, ui.Dim("Following the masir daemon's log. Ctrl-C to stop."))
	}
	for {
		res, err := stream.Recv()
		if err == io.EOF || ctx.Err() != nil {
			return nil
		}
		if err != nil {
			return err
		}
		e := res.Entry
		fmt.Printf("%s %s %s\n", ui.Dim(time.UnixMilli(e.TimestampMs).Format("15:04:05")), levelText(e.Level), ui.Visual(e.Message))
	}
}

func levelText(level string) string {
	l := fmt.Sprintf("[%s]", strings.ToLower(level))
	switch strings.ToLower(level) {
	case "error":
		return ui.Red(l)
	case "warn", "warning":
		return ui.Yellow(l)
	case "debug":
		return ui.Gray(l)
	}
	return ui.Blue(l)
}

func cmdGetConfig(args) error {
	res, err := call(func(ctx context.Context, c pb.DaemonServiceClient) (*pb.GetXrayConfigResponse, error) {
		return c.GetXrayConfig(ctx, &pb.GetXrayConfigRequest{})
	})
	if err != nil {
		return err
	}
	if res.Json == "" {
		return fail("xray isn't running, so there's no config yet; `masir connect` starts it")
	}
	var out bytes.Buffer
	if json.Indent(&out, []byte(res.Json), "", "  ") != nil {
		fmt.Println(res.Json)
		return nil
	}
	fmt.Println(out.String())
	return nil
}
