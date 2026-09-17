package ubot

import (
	"fmt"
	"tgmultibot/ntgcalls"

	tg "github.com/amarnathcjd/gogram/telegram"
)

func (ctx *Context) setCallStatus(call tg.InputGroupCall, state ntgcalls.MediaState) error {
	_, err := ctx.app.PhoneEditGroupCallParticipant(
		&tg.PhoneEditGroupCallParticipantParams{
			Call: call,
			Participant: &tg.InputPeerUser{
				UserID:     ctx.self.ID,
				AccessHash: ctx.self.AccessHash,
			},
			Muted:              state.Muted,
			VideoPaused:        state.VideoPaused,
			VideoStopped:       state.VideoStopped,
			PresentationPaused: state.PresentationPaused,
		},
	)
	if err != nil {
		return fmt.Errorf("PhoneEditGroupCallParticipant failed: %w", err)
	}
	return nil
}
