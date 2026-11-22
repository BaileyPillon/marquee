package decide

import (
	"strings"
	"testing"
)

// An album of 21 tracks: the size checks have something to divide by.
var nocturnes = Want{Album: "Nocturnes", AlbumYear: 1998, Tracks: 21}

func albumSize(name string, size int64) Release {
	return with(rel(name), func(r *Release) { r.Size = size })
}

func TestScoreMusicAlbum(t *testing.T) {
	p := lossless()
	cases := []scoreCase{
		{name: "the right album", profile: p, title: chopin, want: nocturnes,
			release: albumSize("Frédéric Chopin - Nocturnes (1998) [FLAC]", 600*mbSize), accepted: true,
			contains: []string{"Good fit: FLAC, lossless", `It is the album "Nocturnes"`}},
		{name: "an edition note is the same album", profile: p, title: chopin, want: nocturnes,
			release: albumSize("Frédéric Chopin - Nocturnes (Deluxe Edition) (1998) [FLAC]", 700*mbSize), accepted: true},
		{name: "a reissue year is fine", profile: p, title: chopin, want: nocturnes,
			release: albumSize("Chopin - Nocturnes (Remastered 2011) [FLAC]", 600*mbSize), accepted: true},
		{name: "notes after the album are let through", profile: p, title: chopin, want: nocturnes,
			release: albumSize("Chopin - Nocturnes (1998) [FLAC] [Hi-Res] [Japan Edition]", 600*mbSize), accepted: true},
		{name: "the scene way", profile: p, title: chopin, want: nocturnes,
			release: albumSize("Frederic_Chopin-Nocturnes-CD-FLAC-1998-GRP", 600*mbSize), accepted: true},
		{name: "no dash at all", profile: p, title: chopin, want: nocturnes,
			release: albumSize("Frederic Chopin Nocturnes (1998) FLAC", 600*mbSize), accepted: true},
		{name: "another album by the same artist", profile: p, title: chopin, want: nocturnes,
			release:   albumSize("Chopin - Ballades (1998) [FLAC]", 600*mbSize),
			rejection: `Wrong album: this is "Ballades", not "Nocturnes"`},
		{name: "another volume", profile: p, title: chopin, want: nocturnes,
			release:   albumSize("Chopin - Nocturnes Vol 2 (2005) [FLAC]", 600*mbSize),
			rejection: `Wrong album: this is "Nocturnes Vol 2", not "Nocturnes"`},
		{name: "another volume with no dash", profile: p, title: chopin, want: nocturnes,
			release:   albumSize("Frederic Chopin Nocturnes 2 FLAC", 600*mbSize),
			rejection: `Wrong album: this looks like another one named "nocturnes 2"`},
		{name: "a name that does not say the album", profile: p, title: chopin, want: nocturnes,
			release:   albumSize("Frederic Chopin Piano Works FLAC", 600*mbSize),
			rejection: `Wrong album: the name does not say "Nocturnes"`},
		{name: "a discography", profile: p, title: chopin, want: nocturnes,
			release:   albumSize("Chopin - Discography (1990-2015) [FLAC]", 9000*mbSize),
			rejection: `This is a pack of many albums, not just "Nocturnes"`},
		{name: "a complete collection", profile: p, title: chopin, want: nocturnes,
			release:   albumSize("Chopin - Complete Studio Albums [FLAC]", 9000*mbSize),
			rejection: `This is a pack of many albums, not just "Nocturnes"`},
		{name: "without an album the artist is enough", profile: p, title: chopin,
			release: albumSize("Chopin - Ballades (1998) [FLAC]", 600*mbSize), accepted: true},
	}
	for _, c := range cases {
		c.run(t)
	}
}

func TestScoreMusicQuality(t *testing.T) {
	p := lossless()
	low := p
	low.AllowedSources = []string{"FLAC", "MP3-320", "MP3-128"}
	cases := []scoreCase{
		{name: "FLAC", profile: p, title: chopin, want: nocturnes,
			release: albumSize("Chopin - Nocturnes (1998) [FLAC]", 600*mbSize), accepted: true},
		{name: "FLAC 24 bit", profile: p, title: chopin, want: nocturnes,
			release: albumSize("Chopin - Nocturnes (1998) [FLAC 24bit]", 1800*mbSize), accepted: true},
		{name: "MP3 320", profile: p, title: chopin, want: nocturnes,
			release: albumSize("Chopin - Nocturnes (1998) [MP3 320]", 150*mbSize), accepted: true,
			contains: []string{"Good fit: MP3-320, lossy"}},
		{name: "MP3 192 is under the profile list, not under the floor", profile: p, title: chopin, want: nocturnes,
			release:   albumSize("Chopin - Nocturnes (1998) [MP3 192]", 90*mbSize),
			rejection: "Format not allowed: MP3-192 (your profile takes FLAC, MP3-320)"},
		{name: "MP3 128 is under 192", profile: p, title: chopin, want: nocturnes,
			release:   albumSize("Chopin - Nocturnes (1998) [MP3 128]", 60*mbSize),
			rejection: "Too low a quality: MP3-128 is lossy and under 192 kbps"},
		{name: "MP3 128 when the owner lists it", profile: low, title: chopin, want: nocturnes,
			release: albumSize("Chopin - Nocturnes (1998) [MP3 128]", 60*mbSize), accepted: true},
		{name: "the format is not in the name", profile: p, title: chopin, want: nocturnes,
			release:   albumSize("Chopin - Nocturnes (1998)", 600*mbSize),
			rejection: "Format not stated: the name does not say if it is FLAC or MP3"},
		{name: "a rejected word", profile: with0(p, func(q *Profile) { q.RejectedWords = []string{"bootleg"} }), title: chopin,
			want:      nocturnes,
			release:   albumSize("Chopin - Nocturnes (1998) [FLAC] bootleg", 600*mbSize),
			rejection: `Has a word you rejected: "bootleg"`},
	}
	for _, c := range cases {
		c.run(t)
	}
}

func with0(p Profile, f func(*Profile)) Profile { f(&p); return p }

func TestScoreMusicSizePerTrack(t *testing.T) {
	p := lossless()
	cases := []scoreCase{
		{name: "FLAC far too small for 21 tracks", profile: p, title: chopin, want: nocturnes,
			release:   albumSize("Chopin - Nocturnes (1998) [FLAC]", 60*mbSize),
			rejection: "Too small for 21 tracks: 60 MB is about 3 MB a track, too little for lossless music"},
		{name: "MP3 320 far too small for 21 tracks", profile: p, title: chopin, want: nocturnes,
			release:   albumSize("Chopin - Nocturnes (1998) [MP3 320]", 20*mbSize),
			rejection: "Too small for 21 tracks: 20 MB is about 975 KB a track, too little for lossy music"},
		{name: "far too big for one album", profile: p, title: chopin, want: nocturnes,
			release:   albumSize("Chopin - Nocturnes (1998) [FLAC]", 100*gbSize),
			rejection: "Too big for 21 tracks: 100.0 GB is about 4.8 GB a track, more than one album holds"},
		{name: "no track count, no check", profile: p, title: chopin, want: Want{Album: "Nocturnes"},
			release: albumSize("Chopin - Nocturnes (1998) [FLAC]", 60*mbSize), accepted: true},
		{name: "no size, no check", profile: p, title: chopin, want: nocturnes,
			release: albumSize("Chopin - Nocturnes (1998) [FLAC]", 0), accepted: true},
		{name: "a one-track single is judged by its own count", profile: p, title: chopin, want: Want{Album: "Nocturnes", Tracks: 1},
			release: albumSize("Chopin - Nocturnes (1998) [FLAC]", 30*mbSize), accepted: true},
	}
	for _, c := range cases {
		c.run(t)
	}
}

func TestScoreMusicRanking(t *testing.T) {
	p := lossless()
	s := func(name string, size int64) Verdict {
		return score(p, chopin, nocturnes, albumSize(name, size))
	}
	flac, mp3 := s("Chopin - Nocturnes (1998) [FLAC]", 600*mbSize), s("Chopin - Nocturnes (1998) [MP3 320]", 150*mbSize)
	if !flac.Accepted || !mp3.Accepted || flac.Score <= mp3.Score {
		t.Errorf("FLAC should outrank MP3 320: flac %d, mp3 %d", flac.Score, mp3.Score)
	}
	original := s("Chopin - Nocturnes (1998) [FLAC]", 600*mbSize)
	reissue := s("Chopin - Nocturnes (2011) [FLAC]", 600*mbSize)
	if original.Score <= reissue.Score {
		t.Errorf("the original year should rank above a reissue: %d vs %d", original.Score, reissue.Score)
	}
	liked := score(with0(p, func(q *Profile) { q.PreferredWords = []string{"remaster"} }), chopin, nocturnes,
		albumSize("Chopin - Nocturnes (1998) [FLAC] remaster", 600*mbSize))
	if !liked.Accepted || !strings.Contains(strings.Join(liked.Reasons, "|"), `you prefer`) {
		t.Errorf("a preferred word should be noted: %q", liked.Reasons)
	}
}
