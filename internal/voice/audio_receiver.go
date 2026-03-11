package voice

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"strings"
	"sync/atomic"
	"time"

	disgovoice "github.com/disgoorg/disgo/voice"
	"github.com/disgoorg/snowflake/v2"
)

// tolerantAudioReceiver is a custom AudioReceiver that gracefully handles
// DAVE E2EE decryption failures instead of logging every failure as ERROR.
// It skips undecryptable packets and rate-limits error logging.
type tolerantAudioReceiver struct {
	logger       *slog.Logger
	cancelFunc   context.CancelFunc
	opusReceiver disgovoice.OpusFrameReceiver
	conn         disgovoice.Conn

	// Rate-limited error tracking.
	daveErrors  atomic.Int64
	otherErrors atomic.Int64
}

// NewTolerantAudioReceiver creates a custom AudioReceiver that tolerates DAVE decryption errors.
func NewTolerantAudioReceiver(logger *slog.Logger, receiver disgovoice.OpusFrameReceiver, conn disgovoice.Conn) disgovoice.AudioReceiver {
	return &tolerantAudioReceiver{
		logger:       logger,
		opusReceiver: receiver,
		conn:         conn,
	}
}

func (r *tolerantAudioReceiver) Open() {
	go r.run()
}

func (r *tolerantAudioReceiver) run() {
	defer r.logger.Debug("voice: closing tolerant audio receiver")

	ctx, cancel := context.WithCancel(context.Background())
	r.cancelFunc = cancel
	defer cancel()

	// Periodically log DAVE error counts instead of every error.
	go r.logErrorCounts(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			r.receive()
		}
	}
}

func (r *tolerantAudioReceiver) receive() {
	packet, err := r.conn.UDP().ReadPacket()
	if err != nil {
		if errors.Is(err, net.ErrClosed) {
			r.Close()
			return
		}

		// Classify the error: DAVE decryption failures are expected and non-fatal.
		if isDaveError(err) {
			r.daveErrors.Add(1)
			return
		}

		// Other errors are more concerning.
		r.otherErrors.Add(1)
		r.logger.Error("voice: packet read error", "error", err.Error())
		return
	}

	if r.opusReceiver != nil {
		if err = r.opusReceiver.ReceiveOpusFrame(r.conn.UserIDBySSRC(packet.SSRC), packet); err != nil {
			r.logger.Error("voice: opus frame receive error", "error", err.Error())
		}
	}
}

func (r *tolerantAudioReceiver) logErrorCounts(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			dave := r.daveErrors.Swap(0)
			other := r.otherErrors.Swap(0)
			if dave > 0 || other > 0 {
				r.logger.Warn("voice: packet errors (30s window)",
					"dave_decrypt_errors", dave,
					"other_errors", other,
				)
			}
		}
	}
}

func (r *tolerantAudioReceiver) CleanupUser(userID snowflake.ID) {
	r.opusReceiver.CleanupUser(userID)
}

func (r *tolerantAudioReceiver) Close() {
	if r.cancelFunc != nil {
		r.cancelFunc()
	}
	r.opusReceiver.Close()
}

// isDaveError checks if an error is a DAVE E2EE decryption failure.
func isDaveError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "DAVE decrypt") ||
		strings.Contains(msg, "decrypt frame") ||
		strings.Contains(msg, "missing key ratchet")
}
