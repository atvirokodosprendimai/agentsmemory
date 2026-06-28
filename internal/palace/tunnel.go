package palace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
)

// Tunnels are cross-wing links. This file holds their identity, the explicit-tunnel
// CRUD the MCP tools expose, and the entity-tunnel synthesis recompute uses.
// Ported from the frozen palace_graph create_tunnel / list_tunnels / delete_tunnel
// / follow_tunnels and entity_tunnels_for_wing.

// followPreviewLen is how many characters of a connected drawer follow_tunnels
// shows as a preview (frozen used 300).
const followPreviewLen = 300

// canonicalTunnelID is a tunnel's symmetric identity: the two endpoints sorted
// and hashed, so A↔B and B↔A produce the same id and resolve to one record (the
// frozen _canonical_tunnel_id, made collision-safe). NUL bytes separate wing from
// room (\x00) and endpoint from endpoint (\x01); SanitizeName rejects NUL and
// extracted entities never contain one, so no wing/room value can forge a
// separator and make two distinct endpoint pairs hash alike. 16 hex chars.
func canonicalTunnelID(sourceWing, sourceRoom, targetWing, targetRoom string) string {
	src := sourceWing + "\x00" + sourceRoom
	tgt := targetWing + "\x00" + targetRoom
	a, b := src, tgt
	if a > b {
		a, b = b, a
	}
	sum := sha256.Sum256([]byte(a + "\x01" + b))
	return hex.EncodeToString(sum[:])[:16]
}

// TunnelInput is the create_tunnel payload: the two endpoints, an optional label,
// and optional drawer pins on each side.
type TunnelInput struct {
	SourceWing, SourceRoom string
	TargetWing, TargetRoom string
	Label                  string
	SourceDrawerID         string
	TargetDrawerID         string
}

// CreateTunnel creates or updates an explicit cross-wing tunnel. Both endpoints'
// wing/room are validated to exist (a tunnel must connect real locations), the id
// is canonical so re-creating the reverse direction updates the same record, and
// an update preserves the original orientation, created_at and L7 dynamics while
// refreshing the label and updated_at. now stamps the create/update time.
func (s *Service) CreateTunnel(ctx context.Context, teamID string, in TunnelInput, now string) (Tunnel, error) {
	sw, err := SanitizeName(in.SourceWing, "source_wing")
	if err != nil {
		return Tunnel{}, err
	}
	sr, err := SanitizeName(in.SourceRoom, "source_room")
	if err != nil {
		return Tunnel{}, err
	}
	tw, err := SanitizeName(in.TargetWing, "target_wing")
	if err != nil {
		return Tunnel{}, err
	}
	tr, err := SanitizeName(in.TargetRoom, "target_room")
	if err != nil {
		return Tunnel{}, err
	}
	if ok, err := s.roomExists(ctx, teamID, sw, sr); err != nil {
		return Tunnel{}, err
	} else if !ok {
		return Tunnel{}, fmt.Errorf("%w: source room %q does not exist in wing %q", ErrInvalidInput, sr, sw)
	}
	if ok, err := s.roomExists(ctx, teamID, tw, tr); err != nil {
		return Tunnel{}, err
	} else if !ok {
		return Tunnel{}, fmt.Errorf("%w: target room %q does not exist in wing %q", ErrInvalidInput, tr, tw)
	}

	id := canonicalTunnelID(sw, sr, tw, tr)
	// One atomic upsert, not a read-modify-write: insert the tunnel as new, or — if
	// its canonical id already exists — update ONLY the label and updated_at,
	// preserving the first writer's orientation, created_at, drawer pins and L7
	// dynamics. This closes the lost-update race two concurrent create_tunnel calls
	// would otherwise hit (both seeing "not found", the later overwriting the first).
	t := Tunnel{
		ID: id, TeamID: teamID,
		Source: Endpoint{Wing: sw, Room: sr, DrawerID: in.SourceDrawerID},
		Target: Endpoint{Wing: tw, Room: tr, DrawerID: in.TargetDrawerID},
		Label:  in.Label, Kind: TunnelExplicit, CreatedAt: now, Dynamics: initDynamics(now),
	}
	if err := s.repo.UpsertExplicitTunnel(ctx, t, now); err != nil {
		return Tunnel{}, err
	}
	// Return the canonical stored record (its orientation/created_at may predate
	// this call if the tunnel already existed).
	return s.repo.GetTunnel(ctx, teamID, id)
}

// DeleteTunnel removes a tunnel by id, reporting whether it existed.
func (s *Service) DeleteTunnel(ctx context.Context, teamID, id string) (bool, error) {
	return s.repo.DeleteTunnel(ctx, teamID, id)
}

// ListTunnels returns a team's tunnels, optionally filtered to a wing.
func (s *Service) ListTunnels(ctx context.Context, teamID, wing string) ([]Tunnel, error) {
	return s.repo.ListTunnels(ctx, teamID, wing)
}

// TunnelConnection is one result of follow_tunnels: a tunnel leaving or entering a
// location, the place it connects to, and (when the far endpoint pins a drawer) a
// short preview of that drawer.
type TunnelConnection struct {
	Direction     string `json:"direction"` // "outgoing" or "incoming"
	ConnectedWing string `json:"connected_wing"`
	ConnectedRoom string `json:"connected_room"`
	Label         string `json:"label"`
	DrawerID      string `json:"drawer_id,omitempty"`
	TunnelID      string `json:"tunnel_id"`
	DrawerPreview string `json:"drawer_preview,omitempty"`
}

// FollowTunnels returns the tunnels touching a given wing/room: outgoing ones
// (where it is the source) lead to their target, incoming ones (where it is the
// target) come from their source. When the far endpoint pins a drawer, a short
// preview of that drawer is hydrated so an agent can see where the link leads.
func (s *Service) FollowTunnels(ctx context.Context, teamID, wing, room string) ([]TunnelConnection, error) {
	tunnels, err := s.repo.ListTunnels(ctx, teamID, wing)
	if err != nil {
		return nil, err
	}
	var conns []TunnelConnection
	for _, t := range tunnels {
		switch {
		case t.Source.Wing == wing && t.Source.Room == room:
			conns = append(conns, s.connection(ctx, teamID, "outgoing", t, t.Target))
		case t.Target.Wing == wing && t.Target.Room == room:
			conns = append(conns, s.connection(ctx, teamID, "incoming", t, t.Source))
		}
	}
	return conns, nil
}

// connection builds one TunnelConnection toward the far endpoint, hydrating a
// drawer preview when the endpoint pins one and it still exists.
func (s *Service) connection(ctx context.Context, teamID, direction string, t Tunnel, far Endpoint) TunnelConnection {
	c := TunnelConnection{
		Direction: direction, ConnectedWing: far.Wing, ConnectedRoom: far.Room,
		Label: t.Label, DrawerID: far.DrawerID, TunnelID: t.ID,
	}
	if far.DrawerID != "" {
		if d, err := s.repo.Get(ctx, teamID, far.DrawerID); err == nil {
			r := []rune(d.Content)
			if len(r) > followPreviewLen {
				r = r[:followPreviewLen]
			}
			c.DrawerPreview = string(r)
		}
	}
	return c
}

// roomExists reports whether a team has any drawer in (wing, room) — the check
// that keeps a tunnel from pointing at a location that was never filed. It reuses
// the paginated List with a page of one.
func (s *Service) roomExists(ctx context.Context, teamID, wing, room string) (bool, error) {
	list, err := s.repo.List(ctx, teamID, wing, room, 1, 0)
	if err != nil {
		return false, err
	}
	return len(list) > 0, nil
}

// entityTunnelsForWing synthesises a wing's entity tunnels from the full hallway
// set: for every entity that has a hallway in this wing AND in some other wing, it
// links (wing, "entity:<E>") to (otherWing, "entity:<E>"). The synthetic "entity:"
// room namespaces these away from literal rooms. Ported from the frozen
// entity_tunnels_for_wing; topic tunnels (which need a topic registry this server
// does not yet keep) are intentionally not generated.
func entityTunnelsForWing(teamID, wing string, allHallways []Hallway, now string) []Tunnel {
	// entityWings[entity] = set of wings that entity has any hallway in.
	entityWings := map[string]map[string]struct{}{}
	for _, h := range allHallways {
		for _, e := range []string{h.EntityA, h.EntityB} {
			if entityWings[e] == nil {
				entityWings[e] = map[string]struct{}{}
			}
			entityWings[e][h.Wing] = struct{}{}
		}
	}

	var out []Tunnel
	// Only entities that appear in THIS wing can anchor a tunnel from it.
	for entity, wings := range entityWings {
		if _, here := wings[wing]; !here {
			continue
		}
		others := make([]string, 0, len(wings))
		for w := range wings {
			if w != wing {
				others = append(others, w)
			}
		}
		sort.Strings(others)
		room := "entity:" + entity
		for _, other := range others {
			// Build only one direction per unordered wing pair; the canonical id is
			// symmetric so the reverse would collide anyway, but skipping keeps the
			// set minimal and deterministic.
			if wing > other {
				continue
			}
			out = append(out, Tunnel{
				ID:     canonicalTunnelID(wing, room, other, room),
				TeamID: teamID,
				Source: Endpoint{Wing: wing, Room: room},
				Target: Endpoint{Wing: other, Room: room},
				Label:  "shared entity: " + entity, Kind: TunnelEntity, CreatedAt: now, Dynamics: initDynamics(now),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
