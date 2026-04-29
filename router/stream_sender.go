package router

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"
)

const deltaFallbackFlushRunes = 24
const maxBufferedDeltaDelay = 700 * time.Millisecond

type streamReplySender struct {
	writer              streamReplyWriter
	lastPartialSnapshot string
	pendingDelta        strings.Builder
	pendingSince        time.Time
	hasSeenPartial      bool
	hasEmittedChunks    bool
	hasEmittedDone      bool
	lastEmitAt          time.Time
	now                 func() time.Time
	idleLineBreakAfter  time.Duration
	maxBufferDelay      time.Duration
}

func newStreamReplySender(writer streamReplyWriter) *streamReplySender {
	return &streamReplySender{
		writer:             writer,
		now:                time.Now,
		idleLineBreakAfter: 5 * time.Second,
		maxBufferDelay:     maxBufferedDeltaDelay,
	}
}

func (s *streamReplySender) PushDelta(ctx context.Context, delta string) error {
	if s.hasEmittedDone {
		return nil
	}
	if delta == "" {
		return nil
	}

	s.hasSeenPartial = true
	if s.pendingDelta.Len() == 0 {
		s.pendingSince = s.currentTime()
	}
	s.pendingDelta.WriteString(delta)

	flushed, err := s.flushBufferedDelta(ctx, false)
	if err != nil {
		return err
	}
	if !flushed && s.shouldForceFlushPendingDelta() {
		_, err = s.flushBufferedDelta(ctx, true)
		return err
	}
	return nil
}

func (s *streamReplySender) PushPartialSnapshot(ctx context.Context, snapshot string) error {
	if s.hasEmittedDone {
		return nil
	}
	if snapshot == "" {
		return nil
	}

	s.hasSeenPartial = true
	delta, nextSnapshot := resolveDeltaFromSnapshot(s.lastPartialSnapshot, snapshot)
	s.lastPartialSnapshot = nextSnapshot
	if delta == "" {
		return nil
	}

	return s.emitText(ctx, delta, false)
}

func (s *streamReplySender) PushDeliverText(ctx context.Context, text string, isFinal bool) error {
	if s.hasEmittedDone {
		return nil
	}
	if !isFinal {
		return nil
	}

	if s.hasSeenPartial {
		if _, err := s.flushBufferedDelta(ctx, true); err != nil {
			return err
		}
		if text != "" {
			if err := s.PushPartialSnapshot(ctx, text); err != nil {
				return err
			}
		}
		return s.finalize(ctx)
	}

	if strings.TrimSpace(text) == "" {
		return nil
	}

	if err := s.emitText(ctx, text, false); err != nil {
		return err
	}
	return s.finalize(ctx)
}

func (s *streamReplySender) PushInformationalText(ctx context.Context, text string) error {
	if s.hasEmittedDone {
		return nil
	}
	if strings.TrimSpace(text) == "" {
		return nil
	}

	if _, err := s.flushBufferedDelta(ctx, true); err != nil {
		return err
	}

	return s.emitText(ctx, text, false)
}

func (s *streamReplySender) Settle(ctx context.Context) error {
	if _, err := s.flushBufferedDelta(ctx, true); err != nil {
		return err
	}
	return s.finalize(ctx)
}

func (s *streamReplySender) FlushPendingIfDelayed(ctx context.Context) error {
	if !s.shouldForceFlushPendingDelta() {
		return nil
	}

	_, err := s.flushBufferedDelta(ctx, true)
	return err
}

func (s *streamReplySender) finalize(ctx context.Context) error {
	if s.hasEmittedDone {
		return nil
	}
	if !s.hasEmittedChunks {
		return nil
	}
	if err := s.writer.SendChunk(ctx, "", true); err != nil {
		return err
	}
	s.hasEmittedDone = true
	return nil
}

func (s *streamReplySender) emitText(ctx context.Context, content string, markLastDone bool) error {
	if content == "" {
		return nil
	}

	if s.shouldPrependIdleLineBreak(content) {
		content = "\n" + content
	}

	if err := s.writer.SendChunk(ctx, content, markLastDone); err != nil {
		return err
	}
	s.hasEmittedChunks = true
	if s.now != nil {
		s.lastEmitAt = s.now()
	}
	if markLastDone {
		s.hasEmittedDone = true
	}

	return nil
}

func (s *streamReplySender) shouldPrependIdleLineBreak(content string) bool {
	if !s.hasEmittedChunks || s.idleLineBreakAfter <= 0 || strings.HasPrefix(content, "\n") {
		return false
	}

	if s.now == nil || s.lastEmitAt.IsZero() {
		return false
	}

	return s.now().Sub(s.lastEmitAt) >= s.idleLineBreakAfter
}

func (s *streamReplySender) currentTime() time.Time {
	if s.now == nil {
		return time.Time{}
	}
	return s.now()
}

func (s *streamReplySender) shouldForceFlushPendingDelta() bool {
	if s.pendingDelta.Len() == 0 || s.maxBufferDelay <= 0 {
		return false
	}
	if s.pendingSince.IsZero() {
		return false
	}

	now := s.currentTime()
	if now.IsZero() {
		return false
	}

	return now.Sub(s.pendingSince) >= s.maxBufferDelay
}

func (s *streamReplySender) flushBufferedDelta(ctx context.Context, force bool) (bool, error) {
	buffered := s.pendingDelta.String()
	if buffered == "" {
		return false, nil
	}

	flushLen := findDeltaFlushBoundary(buffered, force)
	if flushLen <= 0 {
		return false, nil
	}

	flushText := buffered[:flushLen]
	remainText := buffered[flushLen:]
	s.pendingDelta.Reset()
	s.pendingDelta.WriteString(remainText)
	if remainText == "" {
		s.pendingSince = time.Time{}
	} else {
		s.pendingSince = s.currentTime()
	}

	if err := s.emitText(ctx, flushText, false); err != nil {
		return false, err
	}

	return true, nil
}

type legacyStreamReplyWriter struct {
	send func(content string) error
}

func (w *legacyStreamReplyWriter) SendChunk(ctx context.Context, content string, done bool) error {
	if done && content == "" {
		return nil
	}
	if content == "" {
		return nil
	}

	return w.send(content)
}

func resolveDeltaFromSnapshot(previous, next string) (string, string) {
	if next == "" || next == previous {
		return "", next
	}
	if strings.HasPrefix(next, previous) {
		return next[len(previous):], next
	}
	if strings.HasPrefix(previous, next) {
		return "", next
	}

	commonBytes := 0
	prevIdx, nextIdx := 0, 0
	for prevIdx < len(previous) && nextIdx < len(next) {
		prevRune, prevSize := utf8.DecodeRuneInString(previous[prevIdx:])
		nextRune, nextSize := utf8.DecodeRuneInString(next[nextIdx:])
		if prevRune != nextRune {
			break
		}
		commonBytes = nextIdx + nextSize
		prevIdx += prevSize
		nextIdx += nextSize
	}

	return next[commonBytes:], next
}

func findDeltaFlushBoundary(buffered string, force bool) int {
	if buffered == "" {
		return 0
	}
	if force {
		return len(buffered)
	}

	if idx := strings.LastIndex(buffered, "\n\n"); idx >= 0 {
		return idx + len("\n\n")
	}

	lastBoundary := 0
	runeCount := 0
	for idx, r := range buffered {
		runeCount++
		switch r {
		case '\n':
			lastBoundary = idx + utf8.RuneLen(r)
		case '。', '！', '？', '；', '：', '，', '.', '!', '?', ';':
			lastBoundary = idx + utf8.RuneLen(r)
		}
	}

	if lastBoundary == len(buffered) && runeCount >= 4 {
		return lastBoundary
	}
	if runeCount >= 12 && lastBoundary > 0 {
		return lastBoundary
	}
	if lastBoundary == 0 && runeCount >= deltaFallbackFlushRunes {
		return len(buffered)
	}
	if runeCount >= 220 {
		return len(buffered)
	}

	return 0
}
