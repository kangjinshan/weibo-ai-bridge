package router

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"
)

const deltaFallbackFlushRunes = 24

type streamReplySender struct {
	writer              streamReplyWriter
	lastPartialSnapshot string
	pendingDelta        strings.Builder
	hasSeenPartial      bool
	hasEmittedChunks    bool
	hasEmittedDone      bool
	lastEmitAt          time.Time
	now                 func() time.Time
	idleLineBreakAfter  time.Duration
}

func newStreamReplySender(writer streamReplyWriter) *streamReplySender {
	return &streamReplySender{
		writer:             writer,
		now:                time.Now,
		idleLineBreakAfter: 5 * time.Second,
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
	s.pendingDelta.WriteString(delta)

	return s.flushBufferedDelta(ctx, false)
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
		if err := s.flushBufferedDelta(ctx, true); err != nil {
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

	if err := s.flushBufferedDelta(ctx, true); err != nil {
		return err
	}

	return s.emitText(ctx, text, false)
}

func (s *streamReplySender) Settle(ctx context.Context) error {
	if err := s.flushBufferedDelta(ctx, true); err != nil {
		return err
	}
	return s.finalize(ctx)
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

func (s *streamReplySender) flushBufferedDelta(ctx context.Context, force bool) error {
	buffered := s.pendingDelta.String()
	if buffered == "" {
		return nil
	}

	flushLen := findDeltaFlushBoundary(buffered, force)
	if flushLen <= 0 {
		return nil
	}

	flushText := buffered[:flushLen]
	remainText := buffered[flushLen:]
	s.pendingDelta.Reset()
	s.pendingDelta.WriteString(remainText)

	return s.emitText(ctx, flushText, false)
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

	prefixLen := 0
	maxLen := len(previous)
	if len(next) < maxLen {
		maxLen = len(next)
	}
	for prefixLen < maxLen && previous[prefixLen] == next[prefixLen] {
		prefixLen++
	}

	return next[prefixLen:], next
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
