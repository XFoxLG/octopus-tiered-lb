package relay

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

var errHedgeLost = errors.New("hedge lane cancelled after another lane won")

// A request/policy failure stops speculative routing before client commitment.
// Wrapping retains errors.Is/As and the serial retry classification.
type hedgeTerminalError struct{ error }

func (e *hedgeTerminalError) Unwrap() error { return e.error }

// A lane emits complete protocol frames, preserving tools, choices, multimodal
// blocks and terminal events. Visible is supplied by that protocol's adapter.
// The lane owns its parser and request; it must not persist accounting or touch
// channel health. Those effects belong to the winning result after coordination.
type relayHedgeFrame struct {
	Payload  []byte
	Visible  bool
	Terminal bool
	Headers  http.Header
}
type relayHedgeLane func(context.Context, func(relayHedgeFrame) error) error

type relayHedgeMessage struct {
	lane  int
	frame relayHedgeFrame
	ack   chan error
	done  bool
	err   error
}
type relayHedgeOutcome struct {
	Winner        int // -1 means no successful client delivery.
	StopLane      int // non-retryable outcome before client delivery; -1 otherwise.
	Errors        [2]error
	BackupStarted bool
}

// coordinateRelayHedge exclusively owns writeFlush. A successful call means a
// complete Write + Flush; a failed call aborts both lanes, without switching.
// startBackup reserves capacity and budgets at the moment the delay expires;
// returning nil leaves the primary serial and is not a relay failure.
func coordinateRelayHedge(ctx context.Context, delay time.Duration, primary relayHedgeLane,
	startBackup func() relayHedgeLane, writeFlush func(int, []byte, http.Header) error) (relayHedgeOutcome, error) {
	outcome := relayHedgeOutcome{Winner: -1, StopLane: -1}
	if primary == nil || writeFlush == nil {
		return outcome, errors.New("missing hedge primary or writer")
	}
	messages := make(chan relayHedgeMessage, 8)
	contexts := [2]context.Context{}
	cancels := [2]context.CancelCauseFunc{}
	active := [2]bool{}
	buffers := [2][][]byte{}
	bufferHeaders := [2]http.Header{}
	bufferedBytes := [2]int{}
	terminalSeen := [2]bool{}
	const maxBufferedBytes = 8 << 20
	start := func(index int, lane relayHedgeLane) {
		contexts[index], cancels[index] = context.WithCancelCause(ctx)
		active[index] = true
		laneCtx := contexts[index]
		go func() {
			err := lane(laneCtx, func(frame relayHedgeFrame) error {
				// Copy before handoff: adapters may reuse their next-chunk buffer.
				frame.Payload = append([]byte(nil), frame.Payload...)
				ack := make(chan error, 1)
				select {
				case messages <- relayHedgeMessage{lane: index, frame: frame, ack: ack}:
				case <-laneCtx.Done():
					return context.Cause(laneCtx)
				}
				select {
				case err := <-ack:
					return err
				case <-laneCtx.Done():
					return context.Cause(laneCtx)
				}
			})
			messages <- relayHedgeMessage{lane: index, done: true, err: err}
		}()
	}
	start(0, primary)
	timer := time.NewTimer(max(delay, 0))
	defer timer.Stop()
	timerC := timer.C
	backupConsidered := false
	tryBackup := func() {
		if backupConsidered || outcome.Winner >= 0 || ctx.Err() != nil {
			return
		}
		backupConsidered = true
		timerC = nil
		if startBackup != nil {
			if backup := startBackup(); backup != nil {
				outcome.BackupStarted = true
				start(1, backup)
			}
		}
	}
	var clientErr error
	ctxDone := ctx.Done()
	for active[0] || active[1] {
		select {
		case <-ctxDone:
			clientErr = context.Cause(ctx)
			ctxDone = nil
			timerC = nil
			for i, cancel := range cancels {
				if active[i] && cancel != nil {
					cancel(clientErr)
				}
			}
		case <-timerC:
			tryBackup()
		case msg := <-messages:
			if msg.done {
				active[msg.lane] = false
				outcome.Errors[msg.lane] = msg.err
				if outcome.Winner < 0 && clientErr == nil {
					var terminal *hedgeTerminalError
					if errors.As(msg.err, &terminal) {
						outcome.StopLane = msg.lane
						clientErr = terminal.error
						timerC = nil
						for i, cancel := range cancels {
							if active[i] && cancel != nil {
								cancel(errHedgeLost)
							}
						}
					} else if msg.err == nil && terminalSeen[msg.lane] {
						// A valid intentionally empty completion still owns its native
						// terminal frames. It must not become an empty-output retry.
						payload := make([]byte, 0, bufferedBytes[msg.lane])
						for _, part := range buffers[msg.lane] {
							payload = append(payload, part...)
						}
						clientErr = writeFlush(msg.lane, payload, bufferHeaders[msg.lane])
						if clientErr == nil {
							outcome.Winner = msg.lane
						}
						timerC = nil
						for i, cancel := range cancels {
							if active[i] && cancel != nil {
								cancel(errHedgeLost)
							}
						}
					} else {
						tryBackup()
					}
				}
				continue
			}
			if clientErr != nil {
				msg.ack <- clientErr
				continue
			}
			if outcome.Winner >= 0 && msg.lane != outcome.Winner {
				msg.ack <- errHedgeLost
				continue
			}
			if outcome.Winner < 0 {
				if msg.frame.Terminal {
					terminalSeen[msg.lane] = true
				}
				bufferedBytes[msg.lane] += len(msg.frame.Payload)
				limit := maxBufferedBytes
				if msg.frame.Visible {
					limit += maxSSEEventSize
				}
				if bufferedBytes[msg.lane] > limit {
					err := fmt.Errorf("hedge pre-output buffer exceeds %d bytes", limit)
					cancels[msg.lane](err)
					msg.ack <- err
					continue
				}
				buffers[msg.lane] = append(buffers[msg.lane], msg.frame.Payload)
				bufferHeaders[msg.lane] = msg.frame.Headers
				if (!msg.frame.Visible && !msg.frame.Terminal) || len(msg.frame.Payload) == 0 {
					msg.ack <- nil
					continue
				}
				// Write the buffered protocol prefix and first visible frame
				// atomically with respect to the other lane's client writes.
				payload := make([]byte, 0, bufferedBytes[msg.lane])
				for _, part := range buffers[msg.lane] {
					payload = append(payload, part...)
				}
				clientErr = writeFlush(msg.lane, payload, msg.frame.Headers)
				if clientErr == nil {
					outcome.Winner = msg.lane
					timer.Stop()
					timerC = nil
					other := 1 - msg.lane
					if active[other] {
						cancels[other](errHedgeLost)
					}
					buffers = [2][][]byte{}
				}
			} else {
				clientErr = writeFlush(msg.lane, msg.frame.Payload, msg.frame.Headers)
			}
			if clientErr != nil {
				timerC = nil
				for i, cancel := range cancels {
					if active[i] && cancel != nil {
						cancel(clientErr)
					}
				}
			}
			msg.ack <- clientErr
		}
	}
	for _, cancel := range cancels {
		if cancel != nil {
			cancel(nil)
		}
	}
	if clientErr != nil {
		return outcome, clientErr
	}
	if outcome.Winner >= 0 {
		return outcome, outcome.Errors[outcome.Winner]
	}
	if outcome.Errors[0] == nil && outcome.Errors[1] == nil {
		return outcome, errEmptyOutput
	}
	return outcome, errors.Join(outcome.Errors[0], outcome.Errors[1])
}
