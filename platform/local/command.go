package local

import (
	"context"
	"strings"

	"github.com/kangjinshan/weibo-ai-bridge/platform/weibo"
)

// handleUserCommand wraps a msghub user_command frame as a slash-command
// weibo.Message so it travels through Router.HandleMessage exactly like a
// regular text command would. The Router never has to know it originated
// from a button click rather than a typed command.
func (p *Platform) handleUserCommand(ctx context.Context, evt *UserCommandEvt) {
	if evt == nil {
		return
	}
	command := strings.TrimSpace(evt.Command)
	if !strings.HasPrefix(command, "/") {
		p.logger.Printf("local: ignoring user_command without leading slash: %q", command)
		return
	}

	content := command
	if len(evt.Args) > 0 {
		content = command + " " + strings.Join(evt.Args, " ")
	}

	device := ""
	if evt.Device != nil {
		device = *evt.Device
	}

	msg := &weibo.Message{
		ID:        newRequestID(),
		Type:      weibo.MessageTypeText,
		Content:   content,
		UserID:    evt.ConvID,
		UserName:  device,
		Timestamp: p.now().UnixMilli(),
	}

	select {
	case <-ctx.Done():
		return
	case p.messageChan <- msg:
	}
}
