package model

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"
)

func linkedVideo(t *testing.T) (*Project, string) {
	t.Helper()
	p := NewProject("links")
	m := MediaItem{ID: NewID(), Kind: KindVideo, Name: "camera", HasAudio: true, AudioStreams: 3, Duration: 4 * time.Second}
	p.AddMedia(m)
	id, err := p.PlaceMedia(m, 20, 1, p.Tracks[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	return p, id
}

func TestAllAudioStreamsLinkedAndPersistent(t *testing.T) {
	p, id := linkedVideo(t)
	group := p.ExplicitGroup(id)
	if len(group) != 4 {
		t.Fatalf("group: %v", group)
	}
	streams, tracks := map[int]bool{}, map[int]bool{}
	for _, partner := range group {
		c, ti, _ := p.Clip(partner)
		if c.Kind == KindAudio {
			streams[c.AudioStream] = true
			tracks[ti] = true
		}
	}
	if len(streams) != 3 || len(tracks) != 3 {
		t.Fatalf("streams %v tracks %v", streams, tracks)
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var loaded Project
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(group, loaded.ExplicitGroup(id)) {
		t.Fatal("links lost on save/load")
	}
	if err := loaded.MoveClip(id, loaded.Tracks[0].ID, 30); err != nil {
		t.Fatal(err)
	}
	for _, partner := range group {
		c, _, _ := loaded.Clip(partner)
		if c.Start != 30 {
			t.Fatal("move broke sync")
		}
	}
	if err := loaded.TrimClip(id, 35, 90); err != nil {
		t.Fatal(err)
	}
	for _, partner := range group {
		c, _, _ := loaded.Clip(partner)
		if c.Start != 35 || c.Length != 90 || c.SourceIn != 5 {
			t.Fatalf("trim %+v", c)
		}
	}
	right, err := loaded.SplitClip(id, 70)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.ExplicitGroup(id)) != 4 || len(loaded.ExplicitGroup(right)) != 4 {
		t.Fatal("split groups")
	}
	l, _, _ := loaded.Clip(id)
	r, _, _ := loaded.Clip(right)
	if l.LinkID == r.LinkID {
		t.Fatal("cut sides share a link group")
	}
	if err := loaded.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestManualLinkAndUnlink(t *testing.T) {
	p := NewProject("manual")
	a := NewClip(KindTitle, "title", 10, 20)
	b := NewClip(KindAudio, "sound", 17, 30)
	_ = p.PlaceClip(p.Tracks[0].ID, a)
	_ = p.PlaceClip(p.Tracks[2].ID, b)
	if err := p.LinkClips([]string{a.ID, b.ID}); err != nil {
		t.Fatal(err)
	}
	if len(p.LinkedClips(a.ID)) != 1 {
		t.Fatal("manual partners")
	}
	if err := p.MoveClip(a.ID, p.Tracks[0].ID, 20); err != nil {
		t.Fatal(err)
	}
	c, _, _ := p.Clip(b.ID)
	if c.Start != 27 {
		t.Fatal("relative offset lost")
	}
	if err := p.UnlinkClips([]string{a.ID}); err != nil {
		t.Fatal(err)
	}
	if len(p.LinkedClips(a.ID)) != 0 {
		t.Fatal("unlink failed")
	}
}

func TestLockedPartnersBlockWithoutPartialChanges(t *testing.T) {
	p, id := linkedVideo(t)
	_, ti, _ := p.Clip(p.LinkedClips(id)[0])
	p.Tracks[ti].Locked = true
	for name, edit := range map[string]func() error{
		"trim":   func() error { return p.TrimClip(id, 25, 90) },
		"split":  func() error { _, err := p.SplitClip(id, 50); return err },
		"unlink": func() error { return p.UnlinkClips([]string{id}) },
		"move":   func() error { return p.MoveClip(id, p.Tracks[0].ID, 30) },
	} {
		before := p.Clone()
		if err := edit(); !errors.Is(err, ErrLocked) {
			t.Fatalf("%s: %v", name, err)
		}
		if !reflect.DeepEqual(p, before) {
			t.Fatalf("%s partially modified project", name)
		}
	}
}
