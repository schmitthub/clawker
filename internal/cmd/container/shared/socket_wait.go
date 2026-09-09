package shared

import (
	"strconv"
	"strings"

	"github.com/schmitthub/clawker/internal/socketbridge"
)

const socketWaitTimeoutSeconds = 60

func socketsWaitScript(sockets []socketbridge.BridgedSocket, timeoutSeconds int) string {
	if len(sockets) == 0 {
		return ""
	}
	lines := []string{
		"elapsed=0",
		"while :; do",
		"    missing=''",
	}
	for _, socket := range sockets {
		target := shellQuoteSocketTarget(socket.Target)
		lines = append(lines,
			"    if [ -S "+target+" ]; then",
			"        :",
			"    elif [ -z \"$missing\" ]; then",
			"        missing="+target,
			"    fi",
		)
	}
	lines = append(lines,
		"    [ -z \"$missing\" ] && exit 0",
		"    if [ \"$elapsed\" -ge "+strconv.Itoa(timeoutSeconds)+" ]; then",
		"        printf 'Timed out waiting for socket: %s\\n' \"$missing\"",
		"        exit 1",
		"    fi",
		"    sleep 1",
		"    elapsed=$((elapsed + 1))",
		"done",
	)
	return strings.Join(lines, "\n") + "\n"
}

func shellQuoteSocketTarget(target string) string {
	return "'" + strings.ReplaceAll(target, "'", `'"'"'`) + "'"
}
