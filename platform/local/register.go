package local

import (
	"context"
	"errors"
)

// publishRegistration sends register_agents after a successful connection.
// Errors are returned to the caller; the supervisor logs them and keeps the
// connection alive (the user can still poll /list/agents from clients even
// if registration was rejected — they just won't see this bridge's agents).
func (p *Platform) publishRegistration(ctx context.Context) error {
	if p.deps.Agents == nil {
		return errors.New("local: AgentLister not configured in PlatformDeps")
	}
	agentsList := p.deps.Agents.ListAgents()
	agents := make([]Agent, 0, len(agentsList))
	for _, a := range agentsList {
		if a.ID == "" {
			continue
		}
		agents = append(agents, Agent{
			ID:          a.ID,
			DisplayName: nonEmpty(a.DisplayName, a.ID),
			Status:      "online",
		})
	}

	var commands []CommandMeta
	if p.deps.Commands != nil {
		descriptors := p.deps.Commands.ListCommands()
		commands = make([]CommandMeta, 0, len(descriptors))
		for _, c := range descriptors {
			if c.Name == "" {
				continue
			}
			commands = append(commands, CommandMeta{
				Name:        c.Name,
				Description: c.Description,
				Args:        c.Args,
			})
		}
	}

	_, err := p.request(ctx, FrameRegisterAgents, RegisterAgentsReq{
		Agents:   agents,
		Commands: commands,
	})
	return err
}

// PublishAgentStatus announces an online/offline transition for one agent.
// Currently unused from the main loop — wired up so future agent-availability
// signals can simply call this without re-registering.
func (p *Platform) PublishAgentStatus(ctx context.Context, agentID, status string) error {
	if agentID == "" {
		return errors.New("local: agent_id is required")
	}
	if status != "online" && status != "offline" {
		return errors.New("local: status must be online or offline")
	}
	return p.writeFrame(FrameAgentStatus, "", AgentStatusReq{
		AgentID: agentID,
		Status:  status,
	})
}

func nonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
