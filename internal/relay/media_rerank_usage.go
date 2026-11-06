package relay

import (
	"bytes"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/lingyuins/octopus/internal/helper"
)

const mediaRerankUsageKey = "relay_rerank_usage"
const mediaRerankUsageMaxBytes = 1 << 20

type mediaRerankUsage struct {
	input, output int
	reported      bool
}

func mediaRerankUsageFromContext(c *gin.Context) mediaRerankUsage {
	value, _ := c.Get(mediaRerankUsageKey)
	usage, _ := value.(mediaRerankUsage)
	return usage
}

// This bounded observation never truncates or rewrites the downstream response.
// Oversized or incomplete JSON simply has no trustworthy usage observation.
type mediaRerankUsageCapture struct {
	bytes.Buffer
	oversized bool
}

func (capture *mediaRerankUsageCapture) Write(payload []byte) (int, error) {
	if !capture.oversized {
		if capture.Len()+len(payload) > mediaRerankUsageMaxBytes {
			capture.oversized = true
			capture.Reset()
		} else {
			_, _ = capture.Buffer.Write(payload)
		}
	}
	return len(payload), nil
}

func handleRerankResponse(c *gin.Context, response *http.Response) (int, error) {
	if ct := response.Header.Get("Content-Type"); ct != "" {
		c.Header("Content-Type", ct)
	}
	var capture mediaRerankUsageCapture
	if _, err := io.Copy(c.Writer, io.TeeReader(response.Body, &capture)); err != nil {
		return 0, writeErrorOutcome(c, fmt.Errorf("failed to stream response: %w", err))
	}
	if !capture.oversized {
		c.Set(mediaRerankUsageKey, parseMediaRerankUsage(capture.Bytes()))
	}
	return response.StatusCode, nil
}

func parseMediaRerankUsage(body []byte) mediaRerankUsage {
	input, output, reported := helper.ParseRerankTokenUsage(body)
	return mediaRerankUsage{input: input, output: output, reported: reported}
}
