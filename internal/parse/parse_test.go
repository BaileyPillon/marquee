package parse

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Short names so the tables stay one line per case.
const (
	bd   = SourceBluray
	rmx  = SourceRemux
	wdl  = SourceWEBDL
	wrp  = SourceWEBRip
	htv  = SourceHDTV
	dvd  = SourceDVD
	h264 = CodecH264
	h265 = CodecH265
)

type R = Release

type releaseCase struct {
	in   string
	want Release
}

func js(r Release) string {
	b, _ := json.Marshal(r)
	return string(b)
}

// Every case below is a realistic name for a title the project tests with
// (public-domain films, Blender open movies, Pioneer One) or a made-up name
// that exercises one tricky rule. The group is always the made-up "GRP".
var releaseCases = []releaseCase{
	// ---- movies: the test library -------------------------------------------
	{"Metropolis.1927.1080p.BluRay.x264-GRP.mkv", R{Title: "Metropolis", Year: 1927, Resolution: 1080, Source: bd, Codec: h264, Group: "GRP", Container: "mkv"}},
	{"Metropolis (1927) [1080p].mkv", R{Title: "Metropolis", Year: 1927, Resolution: 1080, Container: "mkv"}},
	{"Metropolis (1927).mkv", R{Title: "Metropolis", Year: 1927, Container: "mkv"}},
	{"Metropolis.1927.German.1080p.BluRay.x264-GRP", R{Title: "Metropolis", Year: 1927, Resolution: 1080, Source: bd, Codec: h264, Languages: []string{"German"}, Group: "GRP"}},
	{"Metropolis.1927.Restored.720p.WEB-DL.AAC2.0.H.264-GRP.mp4", R{Title: "Metropolis", Year: 1927, Resolution: 720, Source: wdl, Codec: h264, Audio: []string{"AAC"}, Channels: "2.0", Edition: "Restored", Group: "GRP", Container: "mp4"}},
	{"Metropolis", R{Title: "Metropolis"}},
	{"metropolis.mkv", R{Title: "metropolis", Container: "mkv"}},
	{"Nosferatu.1922.720p.BluRay.x264-GRP.mkv", R{Title: "Nosferatu", Year: 1922, Resolution: 720, Source: bd, Codec: h264, Group: "GRP", Container: "mkv"}},
	{"Nosferatu (1922)", R{Title: "Nosferatu", Year: 1922}},
	{"Nosferatu.A.Symphony.of.Horror.1922.1080p.BluRay.x264-GRP", R{Title: "Nosferatu A Symphony of Horror", Year: 1922, Resolution: 1080, Source: bd, Codec: h264, Group: "GRP"}},
	{"Nosferatu_1922_Restored_1080p.mp4", R{Title: "Nosferatu", Year: 1922, Resolution: 1080, Edition: "Restored", Container: "mp4"}},
	{"Nosferatu.1922.REMASTERED.1080p.BluRay.x265.10bit.FLAC.2.0-GRP", R{Title: "Nosferatu", Year: 1922, Resolution: 1080, Source: bd, Codec: h265, Audio: []string{"FLAC"}, Channels: "2.0", Edition: "Remastered", Group: "GRP"}},
	{"Sherlock.Jr.1924.720p.WEB-DL.AAC2.0.H.264-GRP.mp4", R{Title: "Sherlock Jr", Year: 1924, Resolution: 720, Source: wdl, Codec: h264, Audio: []string{"AAC"}, Channels: "2.0", Group: "GRP", Container: "mp4"}},
	{"Sherlock Jr. (1924).mkv", R{Title: "Sherlock Jr.", Year: 1924, Container: "mkv"}},
	{"Sherlock Jr 1924 480p.avi", R{Title: "Sherlock Jr", Year: 1924, Resolution: 480, Container: "avi"}},
	{"Night.of.the.Living.Dead.1968.480p.DVDRip.XviD-GRP.avi", R{Title: "Night of the Living Dead", Year: 1968, Resolution: 480, Source: dvd, Codec: CodecXviD, Group: "GRP", Container: "avi"}},
	{"Night.of.the.Living.Dead.1968.2160p.UHD.BluRay.REMUX.HDR10.HEVC.DTS-HD.MA.5.1-GRP", R{Title: "Night of the Living Dead", Year: 1968, Resolution: 2160, Source: rmx, Codec: h265, HDR: []string{"HDR10"}, Audio: []string{"DTS-HD MA"}, Channels: "5.1", Group: "GRP"}},
	{"Night of the Living Dead (1968).mkv", R{Title: "Night of the Living Dead", Year: 1968, Container: "mkv"}},
	{"Night.of.the.Living.Dead.1990.1080p.BluRay.x264-GRP", R{Title: "Night of the Living Dead", Year: 1990, Resolution: 1080, Source: bd, Codec: h264, Group: "GRP"}},
	{"Sintel.2010.1080p.BluRay.x264-GRP", R{Title: "Sintel", Year: 2010, Resolution: 1080, Source: bd, Codec: h264, Group: "GRP"}},
	{"Sintel.2010.4K.UHD.x265.HDR10-GRP.mkv", R{Title: "Sintel", Year: 2010, Resolution: 2160, Codec: h265, HDR: []string{"HDR10"}, Group: "GRP", Container: "mkv"}},
	{"Sintel (2010) [1080p] [BluRay] [x264] [GRP].mkv", R{Title: "Sintel", Year: 2010, Resolution: 1080, Source: bd, Codec: h264, Container: "mkv"}},
	{"Big Buck Bunny (2008)/Big Buck Bunny (2008).mkv", R{Title: "Big Buck Bunny", Year: 2008, Container: "mkv"}},
	{"Big.Buck.Bunny.2008.1080p.WEB-DL.AAC2.0.H.264-GRP.mp4", R{Title: "Big Buck Bunny", Year: 2008, Resolution: 1080, Source: wdl, Codec: h264, Audio: []string{"AAC"}, Channels: "2.0", Group: "GRP", Container: "mp4"}},
	{"Big.Buck.Bunny.2008.720p.BluRay.DD5.1.x264-GRP", R{Title: "Big Buck Bunny", Year: 2008, Resolution: 720, Source: bd, Codec: h264, Audio: []string{"AC3"}, Channels: "5.1", Group: "GRP"}},
	{"Tears.of.Steel.2012.Directors.Cut.1080p.BluRay.DDP5.1.x264-GRP", R{Title: "Tears of Steel", Year: 2012, Resolution: 1080, Source: bd, Codec: h264, Audio: []string{"EAC3"}, Channels: "5.1", Edition: "Director's Cut", Group: "GRP"}},
	{"Tears.of.Steel.2012.1080p.BluRay.x264.DTS-GRP", R{Title: "Tears of Steel", Year: 2012, Resolution: 1080, Source: bd, Codec: h264, Audio: []string{"DTS"}, Group: "GRP"}},
	{"Tears of Steel (2012)/Tears.of.Steel.2012.1080p.mkv", R{Title: "Tears of Steel", Year: 2012, Resolution: 1080, Container: "mkv"}},
	{`D:\Media\Movies\Nosferatu (1922)\Nosferatu.1922.1080p.mkv`, R{Title: "Nosferatu", Year: 1922, Resolution: 1080, Container: "mkv"}},
	{"Undersea.Kingdom.1936.DVDRip.XviD-GRP", R{Title: "Undersea Kingdom", Year: 1936, Source: dvd, Codec: CodecXviD, Group: "GRP"}},
	{"Undersea Kingdom (1936).avi", R{Title: "Undersea Kingdom", Year: 1936, Container: "avi"}},
	{"Undersea.Kingdom.1936.Colorized.720p.WEBRip.x264-GRP", R{Title: "Undersea Kingdom", Year: 1936, Resolution: 720, Source: wrp, Codec: h264, Edition: "Colorized", Group: "GRP"}},

	// ---- movies: numbers and years that belong to the title ----------------
	{"2001.A.Space.Odyssey.1968.1080p.BluRay.x264-GRP", R{Title: "2001 A Space Odyssey", Year: 1968, Resolution: 1080, Source: bd, Codec: h264, Group: "GRP"}},
	{"2001 A Space Odyssey (1968).mkv", R{Title: "2001 A Space Odyssey", Year: 1968, Container: "mkv"}},
	{"2001.A.Space.Odyssey.1080p.BluRay.x264-GRP", R{Title: "2001 A Space Odyssey", Resolution: 1080, Source: bd, Codec: h264, Group: "GRP"}},
	{"2012.2009.1080p.BluRay.x264-GRP", R{Title: "2012", Year: 2009, Resolution: 1080, Source: bd, Codec: h264, Group: "GRP"}},
	{"2012.1080p.BluRay.x264-GRP", R{Title: "2012", Resolution: 1080, Source: bd, Codec: h264, Group: "GRP"}},
	{"2012 (2009).mkv", R{Title: "2012", Year: 2009, Container: "mkv"}},
	{"1917.2019.1080p.BluRay.x264-GRP", R{Title: "1917", Year: 2019, Resolution: 1080, Source: bd, Codec: h264, Group: "GRP"}},
	{"1917 (2019).mkv", R{Title: "1917", Year: 2019, Container: "mkv"}},
	{"2046 (2004).mkv", R{Title: "2046", Year: 2004, Container: "mkv"}},
	{"1984.1984.720p", R{Title: "1984", Year: 1984, Resolution: 720}},
	{"Blade.Runner.2049.2017.1080p.BluRay.x264-GRP", R{Title: "Blade Runner 2049", Year: 2017, Resolution: 1080, Source: bd, Codec: h264, Group: "GRP"}},
	{"Blade Runner 2049 (2017) 2160p.mkv", R{Title: "Blade Runner 2049", Year: 2017, Resolution: 2160, Container: "mkv"}},
	// Known limit: a lone bare year after the title is read as the release year.
	{"Blade.Runner.2049.1080p.BluRay.x264-GRP", R{Title: "Blade Runner", Year: 2049, Resolution: 1080, Source: bd, Codec: h264, Group: "GRP"}},
	{"Title (2019) (2020).mkv", R{Title: "Title", Year: 2019, Container: "mkv"}},
	{"Title (2019) (2020) 1080p", R{Title: "Title", Year: 2019, Resolution: 1080}},
	{"Title - 2019.mkv", R{Title: "Title", Year: 2019, Container: "mkv"}},
	{"Se7en.1995.1080p.BluRay.x264-GRP", R{Title: "Se7en", Year: 1995, Resolution: 1080, Source: bd, Codec: h264, Group: "GRP"}},
	{"Apollo.13.1995.1080p.BluRay.x264-GRP", R{Title: "Apollo 13", Year: 1995, Resolution: 1080, Source: bd, Codec: h264, Group: "GRP"}},
	{"12.Years.a.Slave.2013.1080p.BluRay.x264-GRP", R{Title: "12 Years a Slave", Year: 2013, Resolution: 1080, Source: bd, Codec: h264, Group: "GRP"}},
	{"300.2006.1080p.BluRay.x264-GRP", R{Title: "300", Year: 2006, Resolution: 1080, Source: bd, Codec: h264, Group: "GRP"}},
	{"Spider-Man.2002.1080p.BluRay.x264-GRP", R{Title: "Spider-Man", Year: 2002, Resolution: 1080, Source: bd, Codec: h264, Group: "GRP"}},
	{"Spider-Man (2002).mkv", R{Title: "Spider-Man", Year: 2002, Container: "mkv"}},
	{"Spider-Man", R{Title: "Spider-Man"}},
	{"The.Dark.Web.2019.1080p.WEB-DL.x264-GRP", R{Title: "The Dark Web", Year: 2019, Resolution: 1080, Source: wdl, Codec: h264, Group: "GRP"}},
	{"Charlotte's Web (2006) 1080p BluRay.mkv", R{Title: "Charlotte's Web", Year: 2006, Resolution: 1080, Source: bd, Container: "mkv"}},
	{"The.Final.Cut.2004.1080p.BluRay.x264-GRP", R{Title: "The Final Cut", Year: 2004, Resolution: 1080, Source: bd, Codec: h264, Group: "GRP"}},
	{"Apocalypse.Now.Redux.2001.1080p.BluRay.x264-GRP", R{Title: "Apocalypse Now Redux", Year: 2001, Resolution: 1080, Source: bd, Codec: h264, Group: "GRP"}},
	{"E.T.the.Extra-Terrestrial.1982.1080p.BluRay.x264-GRP", R{Title: "ET the Extra-Terrestrial", Year: 1982, Resolution: 1080, Source: bd, Codec: h264, Group: "GRP"}},
	{"It.Follows.2014.1080p.BluRay.x264-GRP", R{Title: "It Follows", Year: 2014, Resolution: 1080, Source: bd, Codec: h264, Group: "GRP"}},
	{"Dr. Strangelove (1964).mkv", R{Title: "Dr. Strangelove", Year: 1964, Container: "mkv"}},
	{"Dr.Strangelove.or.How.I.Learned.to.Stop.Worrying.1964.1080p", R{Title: "Dr Strangelove or How I Learned to Stop Worrying", Year: 1964, Resolution: 1080}},
	{"Fröhliche Weihnachten (2019).mkv", R{Title: "Fröhliche Weihnachten", Year: 2019, Container: "mkv"}},
	{"Show Name Complete Series (2010-2015) 1080p", R{Title: "Show Name", Year: 2010, Resolution: 1080, Complete: true, FullSeason: true}},
	{"Show Name (2010-2015)", R{Title: "Show Name", Year: 2010}},

	// ---- movies: editions, tags and file noise -------------------------------
	{"Movie.Title.Extended.Cut.2013.1080p.BluRay.x264-GRP", R{Title: "Movie Title", Year: 2013, Resolution: 1080, Source: bd, Codec: h264, Edition: "Extended", Group: "GRP"}},
	{"Movie.Title.2013.Extended.Cut.1080p.BluRay.x264-GRP", R{Title: "Movie Title", Year: 2013, Resolution: 1080, Source: bd, Codec: h264, Edition: "Extended", Group: "GRP"}},
	{"Movie.Title.Extended.Edition.1080p.BluRay.x264-GRP", R{Title: "Movie Title", Resolution: 1080, Source: bd, Codec: h264, Edition: "Extended", Group: "GRP"}},
	{"Movie.Name.UNRATED.1080p", R{Title: "Movie Name", Resolution: 1080, Edition: "Unrated"}},
	{"Movie.Name.2019.UNCUT.1080p.BluRay", R{Title: "Movie Name", Year: 2019, Resolution: 1080, Source: bd, Edition: "Uncut"}},
	{"Movie.2019.IMAX.2160p.UHD.BluRay.x265.HDR.TrueHD.Atmos.7.1-GRP", R{Title: "Movie", Year: 2019, Resolution: 2160, Source: bd, Codec: h265, HDR: []string{"HDR"}, Audio: []string{"TrueHD", "Atmos"}, Channels: "7.1", Edition: "IMAX", Group: "GRP"}},
	{"Movie Name (2019) Criterion Collection 1080p.mkv", R{Title: "Movie Name", Year: 2019, Resolution: 1080, Edition: "Criterion", Container: "mkv"}},
	{"Movie.Name.Criterion.1080p.BluRay", R{Title: "Movie Name", Resolution: 1080, Source: bd, Edition: "Criterion"}},
	{"Movie.Name.2019.Open.Matte.1080p", R{Title: "Movie Name", Year: 2019, Resolution: 1080, Edition: "Open Matte"}},
	{"Movie.Name.1927.Restored.1080p.BluRay", R{Title: "Movie Name", Year: 1927, Resolution: 1080, Source: bd, Edition: "Restored"}},
	{"Movie.Name.Restored.1927.720p", R{Title: "Movie Name", Year: 1927, Resolution: 720, Edition: "Restored"}},
	{"Movie.Name.Colorized.1936.720p", R{Title: "Movie Name", Year: 1936, Resolution: 720, Edition: "Colorized"}},
	{"Movie.Name.2019.3D.1080p.BluRay", R{Title: "Movie Name", Year: 2019, Resolution: 1080, Source: bd, Edition: "3D"}},
	{"Movie.Name.2019.25th.Anniversary.Edition.1080p", R{Title: "Movie Name", Year: 2019, Resolution: 1080, Edition: "Anniversary Edition"}},
	{"Movie.Name.2019.Special.Edition.1080p", R{Title: "Movie Name", Year: 2019, Resolution: 1080, Edition: "Special Edition"}},
	{"Movie.Name.2019.REMASTERED.2160p.UHD.BluRay.x265.HDR.DV.TrueHD.7.1.Atmos-GRP", R{Title: "Movie Name", Year: 2019, Resolution: 2160, Source: bd, Codec: h265, HDR: []string{"HDR", "DV"}, Audio: []string{"TrueHD", "Atmos"}, Channels: "7.1", Edition: "Remastered", Group: "GRP"}},
	{"Movie (2019) {tmdb-12345}.mkv", R{Title: "Movie", Year: 2019, Container: "mkv"}},
	{"Movie [tmdbid-12345].mkv", R{Title: "Movie", Container: "mkv"}},
	{"Movie (2019) [imdbid-tt1234567].mkv", R{Title: "Movie", Year: 2019, Container: "mkv"}},
	{"Movie_Name_2019_1080p_BluRay_x264-GRP.mkv", R{Title: "Movie Name", Year: 2019, Resolution: 1080, Source: bd, Codec: h264, Group: "GRP", Container: "mkv"}},
	{"www.site.com - Movie Name (2019).mkv", R{Title: "Movie Name", Year: 2019, Container: "mkv"}},
	{"[ www.site.org ] - Movie Name (2019).avi", R{Title: "Movie Name", Year: 2019, Container: "avi"}},
	{"Movie Name (2019).en.srt", R{Title: "Movie Name", Year: 2019, Container: "srt"}},
	{"Movie.Name.2019.1080p.WEB.H264-GRP.nfo", R{Title: "Movie Name", Year: 2019, Resolution: 1080, Source: wdl, Codec: h264, Group: "GRP", Container: "nfo"}},
	{"Movie.2019.1080p.BluRay.x264-GRP[rarbg]", R{Title: "Movie", Year: 2019, Resolution: 1080, Source: bd, Codec: h264, Group: "GRP"}},
	{"Movie.2019.1080p.BluRay.x264.mkv.!qB", R{Title: "Movie", Year: 2019, Resolution: 1080, Source: bd, Codec: h264}},
	{"Movie.Name.2019.1080p.BluRay.x264-GRP-sample.mkv", R{Title: "Movie Name", Year: 2019, Resolution: 1080, Source: bd, Codec: h264, Group: "GRP", IsSample: true, Container: "mkv"}},
	{"sample.mkv", R{Title: "sample", IsSample: true, Container: "mkv"}},
	{`D:\Movies\Metropolis (1927)\Sample\metropolis-sample.mkv`, R{Title: "metropolis-sample", IsSample: true, Container: "mkv"}},
	{"Samples/clip.mkv", R{Title: "clip", IsSample: true, Container: "mkv"}},
	{"1080p.mkv", R{Resolution: 1080, Container: "mkv"}},
	{"S01E01.mkv", R{Season: 1, Episodes: []int{1}, Container: "mkv"}},
	{"01.mkv", R{Title: "01", Container: "mkv"}},
	{"Season 1", R{Season: 1, FullSeason: true}},
	{"Season 01", R{Season: 1, FullSeason: true}},
	{"", R{}},

	// ---- resolution -----------------------------------------------------------
	{"Movie.2019.480p.DVDRip.XviD.AC3-GRP", R{Title: "Movie", Year: 2019, Resolution: 480, Source: dvd, Codec: CodecXviD, Audio: []string{"AC3"}, Group: "GRP"}},
	{"Movie.2019.576p.PAL.DVD", R{Title: "Movie", Year: 2019, Resolution: 576, Source: dvd}},
	{"Movie.2019.720p.BluRay.x264-GRP", R{Title: "Movie", Year: 2019, Resolution: 720, Source: bd, Codec: h264, Group: "GRP"}},
	{"Movie.2019.1080p.BluRay.x264-GRP", R{Title: "Movie", Year: 2019, Resolution: 1080, Source: bd, Codec: h264, Group: "GRP"}},
	{"Movie.2019.2160p.BluRay.x265-GRP", R{Title: "Movie", Year: 2019, Resolution: 2160, Source: bd, Codec: h265, Group: "GRP"}},
	{"Movie.2019.4K.HDR.x265", R{Title: "Movie", Year: 2019, Resolution: 2160, Codec: h265, HDR: []string{"HDR"}}},
	{"Movie.2019.UHD.BluRay", R{Title: "Movie", Year: 2019, Resolution: 2160, Source: bd}},
	{"Movie.2019.1920x1080.x264", R{Title: "Movie", Year: 2019, Resolution: 1080, Codec: h264}},
	{"Movie.2019.1280x720.x264", R{Title: "Movie", Year: 2019, Resolution: 720, Codec: h264}},
	{"Movie.2019.1080i.HDTV.MPEG2-GRP", R{Title: "Movie", Year: 2019, Resolution: 1080, Source: SourceRawHD, Codec: CodecMPEG2, Group: "GRP"}},

	// ---- sources --------------------------------------------------------------
	{"Movie.2019.1080p.BluRay.REMUX.AVC.DTS-HD.MA.7.1-GRP", R{Title: "Movie", Year: 2019, Resolution: 1080, Source: rmx, Codec: h264, Audio: []string{"DTS-HD MA"}, Channels: "7.1", Group: "GRP"}},
	{"Movie.2019.BluRay.1080p.DTS-HD.MA.5.1.AVC.REMUX-GRP", R{Title: "Movie", Year: 2019, Resolution: 1080, Source: rmx, Codec: h264, Audio: []string{"DTS-HD MA"}, Channels: "5.1", Group: "GRP"}},
	{"Movie.2019.1080p.DVDRemux", R{Title: "Movie", Year: 2019, Resolution: 1080, Source: dvd}},
	{"Movie.2019.WEBRip.x264-GRP", R{Title: "Movie", Year: 2019, Source: wrp, Codec: h264, Group: "GRP"}},
	{"Movie.2019.HDRip.XviD-GRP", R{Title: "Movie", Year: 2019, Source: wrp, Codec: CodecXviD, Group: "GRP"}},
	{"Movie.2019.BDRip.x264-GRP", R{Title: "Movie", Year: 2019, Source: bd, Codec: h264, Group: "GRP"}},
	{"Movie.2019.BRRip.XviD-GRP", R{Title: "Movie", Year: 2019, Source: bd, Codec: CodecXviD, Group: "GRP"}},
	{"Movie.2019.BD.1080p.x265", R{Title: "Movie", Year: 2019, Resolution: 1080, Source: bd, Codec: h265}},
	{"Movie.2019.BD25", R{Title: "Movie", Year: 2019, Source: bd}},
	{"The.Movie.2019.1080p.WEBRip.x264-GRP", R{Title: "The Movie", Year: 2019, Resolution: 1080, Source: wrp, Codec: h264, Group: "GRP"}},
	{"Movie.2019.1080p.hdtv.x264-grp", R{Title: "Movie", Year: 2019, Resolution: 1080, Source: htv, Codec: h264, Group: "grp"}},
	{"Movie.2019.DVD-R", R{Title: "Movie", Year: 2019, Source: dvd}},
	{"Movie.2019.NTSC.DVD", R{Title: "Movie", Year: 2019, Source: dvd}},
	{"Movie.2019.PAL.DVD.DD2.0.x264", R{Title: "Movie", Year: 2019, Source: dvd, Codec: h264, Audio: []string{"AC3"}, Channels: "2.0"}},
	{"Movie.2019.CAM.x264-GRP", R{Title: "Movie", Year: 2019, Source: SourceCAM, Codec: h264, Group: "GRP"}},
	{"Movie.2019.HDCAM.x264", R{Title: "Movie", Year: 2019, Source: SourceCAM, Codec: h264}},
	{"Movie.2019.TELESYNC.x264", R{Title: "Movie", Year: 2019, Source: SourceTelesync, Codec: h264}},
	{"Movie.2019.DVDSCR.XviD", R{Title: "Movie", Year: 2019, Source: SourceScreener, Codec: CodecXviD}},
	{"Movie.2019.RAW-HD.1080i", R{Title: "Movie", Year: 2019, Resolution: 1080, Source: SourceRawHD}},
	{"Movie.2019.1080i.HDTV.DD5.1.ts", R{Title: "Movie", Year: 2019, Resolution: 1080, Source: SourceRawHD, Audio: []string{"AC3"}, Channels: "5.1", Container: "ts"}},
	{"Movie (2019) Bluray-1080p.mkv", R{Title: "Movie", Year: 2019, Resolution: 1080, Source: bd, Container: "mkv"}},
	{"Movie (2019) WEBDL-2160p.mkv", R{Title: "Movie", Year: 2019, Resolution: 2160, Source: wdl, Container: "mkv"}},
	{"Movie (2019) Remux-1080p.mkv", R{Title: "Movie", Year: 2019, Resolution: 1080, Source: rmx, Container: "mkv"}},
	{"Movie (2019) HDTV-720p.mkv", R{Title: "Movie", Year: 2019, Resolution: 720, Source: htv, Container: "mkv"}},
	{"Movie (2019) WEBRip-480p.mkv", R{Title: "Movie", Year: 2019, Resolution: 480, Source: wrp, Container: "mkv"}},

	// ---- codecs, HDR, audio, flags, languages ---------------------------------
	{"Movie Name (2019) - 1080p BluRay x265 HEVC 10bit AAC 5.1.mkv", R{Title: "Movie Name", Year: 2019, Resolution: 1080, Source: bd, Codec: h265, Audio: []string{"AAC"}, Channels: "5.1", Container: "mkv"}},
	{"Movie.2019.AV1.1080p.WEB-DL.OPUS.5.1-GRP", R{Title: "Movie", Year: 2019, Resolution: 1080, Source: wdl, Codec: CodecAV1, Audio: []string{"Opus"}, Channels: "5.1", Group: "GRP"}},
	{"Movie.2019.2160p.WEB-DL.DDP5.1.DV.HEVC-GRP", R{Title: "Movie", Year: 2019, Resolution: 2160, Source: wdl, Codec: h265, HDR: []string{"DV"}, Audio: []string{"EAC3"}, Channels: "5.1", Group: "GRP"}},
	{"Movie.2019.2160p.WEB-DL.DoVi.HDR10Plus.HEVC", R{Title: "Movie", Year: 2019, Resolution: 2160, Source: wdl, Codec: h265, HDR: []string{"DV", "HDR10+"}}},
	{"Movie.2019.HLG.2160p", R{Title: "Movie", Year: 2019, Resolution: 2160, HDR: []string{"HLG"}}},
	{"Movie.2019.1080p.WEB-DL.DD+5.1.H.264-GRP", R{Title: "Movie", Year: 2019, Resolution: 1080, Source: wdl, Codec: h264, Audio: []string{"EAC3"}, Channels: "5.1", Group: "GRP"}},
	{"Movie.2019.1080p.WEB-DL.DD2.0.H.264-GRP", R{Title: "Movie", Year: 2019, Resolution: 1080, Source: wdl, Codec: h264, Audio: []string{"AC3"}, Channels: "2.0", Group: "GRP"}},
	{"Movie.2019.1080p.BluRay.FLAC.2.0.x264-GRP", R{Title: "Movie", Year: 2019, Resolution: 1080, Source: bd, Codec: h264, Audio: []string{"FLAC"}, Channels: "2.0", Group: "GRP"}},
	{"Movie.2019.1080p.BluRay.AAC.5.1.x264-GRP", R{Title: "Movie", Year: 2019, Resolution: 1080, Source: bd, Codec: h264, Audio: []string{"AAC"}, Channels: "5.1", Group: "GRP"}},
	{"Movie.2019.1080p.BluRay.AC3.x264-GRP", R{Title: "Movie", Year: 2019, Resolution: 1080, Source: bd, Codec: h264, Audio: []string{"AC3"}, Group: "GRP"}},
	{"Movie.2019.1080p.BluRay.DTS.x264-GRP", R{Title: "Movie", Year: 2019, Resolution: 1080, Source: bd, Codec: h264, Audio: []string{"DTS"}, Group: "GRP"}},
	{"Movie.2019.1080p.BluRay.DTS-X.7.1.x265-GRP", R{Title: "Movie", Year: 2019, Resolution: 1080, Source: bd, Codec: h265, Audio: []string{"DTS-X"}, Channels: "7.1", Group: "GRP"}},
	{"Movie.2019.1080p.BluRay.E-AC-3.5.1.x265", R{Title: "Movie", Year: 2019, Resolution: 1080, Source: bd, Codec: h265, Audio: []string{"EAC3"}, Channels: "5.1"}},
	{"Movie.2019.PROPER.1080p.BluRay.x264-GRP", R{Title: "Movie", Year: 2019, Resolution: 1080, Source: bd, Codec: h264, Proper: true, Group: "GRP"}},
	{"Movie.2019.REAL.PROPER.1080p.BluRay.x264-GRP", R{Title: "Movie", Year: 2019, Resolution: 1080, Source: bd, Codec: h264, Proper: true, Real: true, Group: "GRP"}},
	{"Movie.2019.REPACK.1080p.BluRay.x264-GRP", R{Title: "Movie", Year: 2019, Resolution: 1080, Source: bd, Codec: h264, Repack: true, RepackVersion: 1, Group: "GRP"}},
	{"Movie.2019.RERIP.1080p", R{Title: "Movie", Year: 2019, Resolution: 1080, Repack: true, RepackVersion: 1}},
	{"Movie.2019.MULTi.1080p.BluRay.x264-GRP", R{Title: "Movie", Year: 2019, Resolution: 1080, Source: bd, Codec: h264, Languages: []string{"Multi"}, Group: "GRP"}},
	{"Movie.Name.FRENCH.1080p.BluRay.x264-GRP", R{Title: "Movie Name", Resolution: 1080, Source: bd, Codec: h264, Languages: []string{"French"}, Group: "GRP"}},
	{"Movie.2019.GERMAN.DL.1080p.BluRay.x264-GRP", R{Title: "Movie", Year: 2019, Resolution: 1080, Source: bd, Codec: h264, Languages: []string{"German"}, Group: "GRP"}},
	{"Movie.2019.1080p.BluRay.x264.DUAL-GRP", R{Title: "Movie", Year: 2019, Resolution: 1080, Source: bd, Codec: h264, Languages: []string{"Dual"}, Group: "GRP"}},
	{"Movie.2019.1080p.BluRay.x264.MULTi.VFF-GRP", R{Title: "Movie", Year: 2019, Resolution: 1080, Source: bd, Codec: h264, Languages: []string{"Multi", "French"}, Group: "GRP"}},
	{"Movie.2019.TRUEFRENCH.1080p", R{Title: "Movie", Year: 2019, Resolution: 1080, Languages: []string{"French"}}},
	{"Movie.2019.1080p.hevc.xvid", R{Title: "Movie", Year: 2019, Resolution: 1080, Codec: h265}},

	// ---- TV: Pioneer One ------------------------------------------------------
	{"Pioneer.One.S01E01.720p.HDTV.x264-GRP", R{Title: "Pioneer One", Season: 1, Episodes: []int{1}, Resolution: 720, Source: htv, Codec: h264, Group: "GRP"}},
	{"Pioneer.One.S01E02.720p.HDTV.x264-GRP.mkv", R{Title: "Pioneer One", Season: 1, Episodes: []int{2}, Resolution: 720, Source: htv, Codec: h264, Group: "GRP", Container: "mkv"}},
	{"Pioneer One - S01E03 - Alone.mkv", R{Title: "Pioneer One", Season: 1, Episodes: []int{3}, Container: "mkv"}},
	{"Pioneer One (2010) - S01E04 - Alone [HDTV-720p].mkv", R{Title: "Pioneer One", Year: 2010, Season: 1, Episodes: []int{4}, Resolution: 720, Source: htv, Container: "mkv"}},
	{"Pioneer.One.S01E05.XviD-GRP", R{Title: "Pioneer One", Season: 1, Episodes: []int{5}, Codec: CodecXviD, Group: "GRP"}},
	{"Pioneer.One.S01E06.1080p.WEB.H264-GRP", R{Title: "Pioneer One", Season: 1, Episodes: []int{6}, Resolution: 1080, Source: wdl, Codec: h264, Group: "GRP"}},
	{"Pioneer One (2010)/Season 1/Pioneer.One.S01E01.720p.mkv", R{Title: "Pioneer One", Season: 1, Episodes: []int{1}, Resolution: 720, Container: "mkv"}},
	{`C:\Library\Pioneer One\Season 1\Pioneer One - S01E02.mkv`, R{Title: "Pioneer One", Season: 1, Episodes: []int{2}, Container: "mkv"}},
	{"pioneer.one.s01e03.720p.hdtv.x264-grp", R{Title: "pioneer one", Season: 1, Episodes: []int{3}, Resolution: 720, Source: htv, Codec: h264, Group: "grp"}},
	{"Pioneer.One.S01E03E04.720p.WEB.H264-GRP", R{Title: "Pioneer One", Season: 1, Episodes: []int{3, 4}, Resolution: 720, Source: wdl, Codec: h264, Group: "GRP"}},
	{"Pioneer.One.S01E01-E03.1080p", R{Title: "Pioneer One", Season: 1, Episodes: []int{1, 2, 3}, Resolution: 1080}},
	{"Pioneer.One.S01E05-06.720p", R{Title: "Pioneer One", Season: 1, Episodes: []int{5, 6}, Resolution: 720}},
	{"Pioneer.One.S01.Complete.1080p.WEB-DL.x264-GRP", R{Title: "Pioneer One", Season: 1, FullSeason: true, Complete: true, Resolution: 1080, Source: wdl, Codec: h264, Group: "GRP"}},
	{"Pioneer One Season 1 Episode 6 720p", R{Title: "Pioneer One", Season: 1, Episodes: []int{6}, Resolution: 720}},
	{"Pioneer.One.S01.720p.HDTV.x264-GRP", R{Title: "Pioneer One", Season: 1, FullSeason: true, Resolution: 720, Source: htv, Codec: h264, Group: "GRP"}},
	{"Pioneer One Season 1", R{Title: "Pioneer One", Season: 1, FullSeason: true}},
	{"Pioneer One (2010)", R{Title: "Pioneer One", Year: 2010}},

	// ---- TV: other shapes ------------------------------------------------------
	{"Show.Name.2019.S03E10.1080p.NF.WEB-DL.DDP5.1.Atmos.HDR.HEVC-GRP", R{Title: "Show Name", Year: 2019, Season: 3, Episodes: []int{10}, Resolution: 1080, Source: wdl, Codec: h265, HDR: []string{"HDR"}, Audio: []string{"EAC3", "Atmos"}, Channels: "5.1", Service: "NF", Group: "GRP"}},
	{"Show.Name.S01E01.2160p.WEB-DL.DV.HDR10+.H.265-GRP", R{Title: "Show Name", Season: 1, Episodes: []int{1}, Resolution: 2160, Source: wdl, Codec: h265, HDR: []string{"DV", "HDR10+"}, Group: "GRP"}},
	{"Show.Name.S01E01.1080p.AMZN.WEB-DL.DDP5.1.H.264-GRP", R{Title: "Show Name", Season: 1, Episodes: []int{1}, Resolution: 1080, Source: wdl, Codec: h264, Audio: []string{"EAC3"}, Channels: "5.1", Service: "AMZN", Group: "GRP"}},
	{"Show.Name.S01.1080p.BluRay.DTS-HD.MA.5.1.x264-GRP", R{Title: "Show Name", Season: 1, FullSeason: true, Resolution: 1080, Source: bd, Codec: h264, Audio: []string{"DTS-HD MA"}, Channels: "5.1", Group: "GRP"}},
	{"Show.Name.S01-S03.COMPLETE.720p.HDTV", R{Title: "Show Name", Season: 1, Seasons: []int{1, 2, 3}, FullSeason: true, Complete: true, Resolution: 720, Source: htv}},
	{"Show Name Seasons 1-3 720p", R{Title: "Show Name", Season: 1, Seasons: []int{1, 2, 3}, FullSeason: true, Resolution: 720}},
	{"Show.Name.1x05.Episode.Title.avi", R{Title: "Show Name", Season: 1, Episodes: []int{5}, Container: "avi"}},
	{"Show.Name.01x05-06.HDTV.XviD-GRP", R{Title: "Show Name", Season: 1, Episodes: []int{5, 6}, Source: htv, Codec: CodecXviD, Group: "GRP"}},
	{"Show.Name.S00E01.Special.720p", R{Title: "Show Name", Season: 0, Episodes: []int{1}, Resolution: 720}},
	{"Show.Name.S01E01E02E03.720p", R{Title: "Show Name", Season: 1, Episodes: []int{1, 2, 3}, Resolution: 720}},
	{"Show.Name.S01E01-03.720p", R{Title: "Show Name", Season: 1, Episodes: []int{1, 2, 3}, Resolution: 720}},
	{"Show.Name.S01E10-E12.720p", R{Title: "Show Name", Season: 1, Episodes: []int{10, 11, 12}, Resolution: 720}},
	{"Show.Name.s01e01.720p", R{Title: "Show Name", Season: 1, Episodes: []int{1}, Resolution: 720}},
	{"Show Name - S01E01-E02 - Title", R{Title: "Show Name", Season: 1, Episodes: []int{1, 2}}},
	{"Show Name 2010 S01E01", R{Title: "Show Name", Year: 2010, Season: 1, Episodes: []int{1}}},
	{"Show.Name.(2010).S01E01", R{Title: "Show Name", Year: 2010, Season: 1, Episodes: []int{1}}},
	{"Show Name S1E1 720p", R{Title: "Show Name", Season: 1, Episodes: []int{1}, Resolution: 720}},
	{"Show.Name.S01E01.PROPER.720p.HDTV.x264-GRP", R{Title: "Show Name", Season: 1, Episodes: []int{1}, Resolution: 720, Source: htv, Codec: h264, Proper: true, Group: "GRP"}},
	{"Show.Name.S02E05.REPACK2.1080p.WEB.H264-GRP", R{Title: "Show Name", Season: 2, Episodes: []int{5}, Resolution: 1080, Source: wdl, Codec: h264, Repack: true, RepackVersion: 2, Group: "GRP"}},
	{"Show.Name.S01E01.720p.WEB.DL.AAC2.0.H.264-GRP", R{Title: "Show Name", Season: 1, Episodes: []int{1}, Resolution: 720, Source: wdl, Codec: h264, Audio: []string{"AAC"}, Channels: "2.0", Group: "GRP"}},
	{"Show.Name.S01E01.The.Episode.720p.HDTV.x264-GRP", R{Title: "Show Name", Season: 1, Episodes: []int{1}, Resolution: 720, Source: htv, Codec: h264, Group: "GRP"}},
	{"The.Office.US.S02E03.720p", R{Title: "The Office US", Season: 2, Episodes: []int{3}, Resolution: 720}},
	{"Marvel's.Agents.of.S.H.I.E.L.D.S02E01.1080p", R{Title: "Marvel's Agents of SHIELD", Season: 2, Episodes: []int{1}, Resolution: 1080}},
	{"Agents.of.S.H.I.E.L.D.S01E01.720p.HDTV.x264-GRP", R{Title: "Agents of SHIELD", Season: 1, Episodes: []int{1}, Resolution: 720, Source: htv, Codec: h264, Group: "GRP"}},
	{"Love, Death & Robots S01E01.mkv", R{Title: "Love, Death & Robots", Season: 1, Episodes: []int{1}, Container: "mkv"}},
	{"Mr. Robot (2015) - S01E01 - eps1.0_hellofriend.mov.mkv", R{Title: "Mr. Robot", Year: 2015, Season: 1, Episodes: []int{1}, Container: "mkv"}},
	{"1883.S01E01.1080p.WEB.H264-GRP", R{Title: "1883", Season: 1, Episodes: []int{1}, Resolution: 1080, Source: wdl, Codec: h264, Group: "GRP"}},

	// ---- TV: daily shows and anime --------------------------------------------
	{"The.Daily.Show.2019.03.14.720p.WEB.h264-GRP", R{Title: "The Daily Show", AirDate: "2019-03-14", Resolution: 720, Source: wdl, Codec: h264, Group: "GRP"}},
	{"Show.Name.2019.03.14.mkv", R{Title: "Show Name", AirDate: "2019-03-14", Container: "mkv"}},
	{"Show Name - 2019-03-14 - Episode Title.mkv", R{Title: "Show Name", AirDate: "2019-03-14", Container: "mkv"}},
	{"Show.Name.14.03.2019.mkv", R{Title: "Show Name", AirDate: "2019-03-14", Container: "mkv"}},
	{"Show.Name.03.14.2019.mkv", R{Title: "Show Name", AirDate: "2019-03-14", Container: "mkv"}},
	{"Show.Name.01.02.2019.mkv", R{Title: "Show Name 01 02", Year: 2019, Container: "mkv"}},
	{"[Group] Some Anime - 07 [1080p].mkv", R{Title: "Some Anime", AbsoluteEpisode: 7, Resolution: 1080, Group: "Group", Container: "mkv"}},
	{"[Group] Some Anime - 07v2 [1080p][ABCD1234].mkv", R{Title: "Some Anime", AbsoluteEpisode: 7, Version: 2, Resolution: 1080, Group: "Group", Container: "mkv"}},
	{"[SubsPlease] Show Name - 12 (1080p) [ABCD1234].mkv", R{Title: "Show Name", AbsoluteEpisode: 12, Resolution: 1080, Group: "SubsPlease", Container: "mkv"}},
	{"[Erai-raws] Show Name - 12 [1080p][Multiple Subtitle].mkv", R{Title: "Show Name", AbsoluteEpisode: 12, Resolution: 1080, Group: "Erai-raws", Container: "mkv"}},
	{"[Group] Show Name (2019) - 01 [BD 1080p x265 FLAC].mkv", R{Title: "Show Name", Year: 2019, AbsoluteEpisode: 1, Resolution: 1080, Source: bd, Codec: h265, Audio: []string{"FLAC"}, Group: "Group", Container: "mkv"}},
	{"[Group] Show Name S02E05 [1080p].mkv", R{Title: "Show Name", Season: 2, Episodes: []int{5}, Resolution: 1080, Group: "Group", Container: "mkv"}},
	{"[1080p] Movie Name (2019).mkv", R{Title: "Movie Name", Year: 2019, Resolution: 1080, Container: "mkv"}},
	{"Show Name - 100.mkv", R{Title: "Show Name", AbsoluteEpisode: 100, Container: "mkv"}},
	{"Show Name - 1100.mkv", R{Title: "Show Name", AbsoluteEpisode: 1100, Container: "mkv"}},
	{"One.Piece.E1013.1080p", R{Title: "One Piece", AbsoluteEpisode: 1013, Resolution: 1080}},
	{"Show.Name.Ep07.720p", R{Title: "Show Name", AbsoluteEpisode: 7, Resolution: 720}},
}

func TestParseRelease(t *testing.T) {
	if len(releaseCases) < 150 {
		t.Fatalf("only %d release cases; the brief asks for 150 or more", len(releaseCases))
	}
	for _, c := range releaseCases {
		t.Run(c.in, func(t *testing.T) {
			got := ParseRelease(c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("ParseRelease(%q)\n got  %s\n want %s", c.in, js(got), js(c.want))
			}
		})
	}
}

func TestParseReleaseNeverPanics(t *testing.T) {
	nasty := []string{
		"", " ", ".", "..", "-", "[", "]", "[]", "[[", "(", ")", "()", "{", "}", "S", "E", "S01", "SE", "S01E", "1x",
		"x264", "-GRP", "[GRP]", "[GRP] ", "[ ] - ", "www.", "www.a.b -", "\\", "/", "//", `C:\`, "a/b/", "...mkv", ".mkv",
		"mkv", "Sample", "sample-", "-sample", "1080p", "p", "2019", "(2019)", "[2019]", "2019.2019.2019", "1900", "2099", "2100",
		"S99999E99999", "S01E01E02E03E04E05E06E07E08E09E10E11E12E13E14E15", "S01E01-999", "S01E999-1", "E1-E999999999999999999999",
		"Ünïcödé.Tîtle.2019.1080p", "日本語のタイトル (2019).mkv", "Фильм.2019.1080p.BluRay.x264-GRP", "😀.2019", "\x00\xff\xfe", "a\tb\nc",
		strings.Repeat("a", 5000), strings.Repeat("S01E01.", 400), strings.Repeat("[", 3000), strings.Repeat("-", 3000), strings.Repeat("2019 ", 800),
	}
	for _, in := range nasty {
		_ = ParseRelease(in)
		_ = ParseMusic(in)
		_ = Normalize(in)
	}
}

func TestIsTV(t *testing.T) {
	for in, want := range map[string]bool{
		"Pioneer.One.S01E01.720p":        true,
		"Pioneer.One.S01.720p":           true,
		"Show.2019.03.14":                true,
		"[Group] Show - 07 [1080p]":      true,
		"Sintel.2010.1080p.BluRay":       false,
		"Metropolis (1927)":              false,
		"Show.Name.Complete.Series.720p": true,
	} {
		if got := ParseRelease(in).IsTV(); got != want {
			t.Errorf("IsTV(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestQualityName(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"Movie.2019.1080p.BluRay.x264-GRP", "Bluray-1080p"},
		{"Movie.2019.720p.BluRay.x264-GRP", "Bluray-720p"},
		{"Movie.2019.2160p.WEB-DL.DDP5.1.H.265-GRP", "WEBDL-2160p"},
		{"Movie.2019.1080p.WEB-DL.H.264-GRP", "WEBDL-1080p"},
		{"Movie.2019.1080p.WEBRip.x264-GRP", "WEBRip-1080p"},
		{"Movie.2019.2160p.UHD.BluRay.REMUX.HDR.HEVC-GRP", "Remux-2160p"},
		{"Movie.2019.1080p.BluRay.REMUX.AVC-GRP", "Remux-1080p"},
		{"Show.S01E01.720p.HDTV.x264-GRP", "HDTV-720p"},
		{"Show.S01E01.1080p.HDTV.x264-GRP", "HDTV-1080p"},
		{"Show.S01E01.HDTV.XviD-GRP", "SDTV"},
		{"Show.S01E01.PDTV.XviD-GRP", "SDTV"},
		{"Movie.2019.DVDRip.XviD-GRP", "DVD"},
		{"Movie.2019.1080i.HDTV.MPEG2-GRP", "Raw-HD"},
		{"Movie.2019.CAM.x264", "CAM"},
		{"Movie.2019.TELESYNC.x264", "TELESYNC"},
		{"Movie.2019.DVDSCR.XviD", "DVDSCR"},
		{"Movie.2019.BDRip.x264", "Bluray-1080p"}, // no resolution stated: Blu-ray means 1080p
		{"Movie.2019.1080p.x264", "Unknown-1080p"},
		{"Movie.2019", "Unknown"},
		{"", "Unknown"},
	}
	for _, c := range cases {
		if got := QualityName(ParseRelease(c.in)); got != c.want {
			t.Errorf("QualityName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// ---- Normalize --------------------------------------------------------------

func TestNormalize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Metropolis", "metropolis"},
		{"METROPOLIS", "metropolis"},
		{"The Metropolis", "metropolis"},
		{"A Trip to the Moon", "trip to the moon"},
		{"An American Werewolf in London", "american werewolf in london"},
		{"The", "the"},
		{"Fröhlich", "frohlich"},
		{"Préludes", "preludes"},
		{"Études", "etudes"},
		{"Frédéric Chopin", "frederic chopin"},
		{"Dvořák", "dvorak"},
		{"Björk", "bjork"},
		{"Mötley Crüe", "motley crue"},
		{"Beyoncé", "beyonce"},
		{"Sigur Rós", "sigur ros"},
		{"Straße", "strasse"},
		{"Œuvres", "oeuvres"},
		{"Ærø", "aero"},
		{"Łódź", "lodz"},
		{"Rock & Roll", "rock and roll"},
		{"Rock&Roll", "rock and roll"},
		{"Rock and Roll", "rock and roll"},
		{"Tom & Jerry", "tom and jerry"},
		{"Don't Look Up", "dont look up"},
		{"Don’t Look Up", "dont look up"},
		{"Charlotte's Web", "charlottes web"},
		{"Sherlock Jr.", "sherlock jr"},
		{"Sherlock, Jr.", "sherlock jr"},
		{"Spider-Man: Homecoming", "spider man homecoming"},
		{"Spider Man Homecoming", "spider man homecoming"},
		{"S.H.I.E.L.D.", "shield"},
		{"Agents of S.H.I.E.L.D.", "agents of shield"},
		{"Mr. Robot", "mr robot"},
		{"  Extra   spaces\there  ", "extra spaces here"},
		{"Night_of_the_Living_Dead", "night of the living dead"},
		{"(500) Days of Summer", "500 days of summer"},
		{"2001: A Space Odyssey", "2001 a space odyssey"},
		{"Nocturne in E-flat major, Op. 9 No. 2", "nocturne in e flat major op 9 no 2"},
		{"e\u0301cole", "ecole"}, // decomposed é
		{"Привет, Мир", "привет мир"},
		{"日本語", "日本語"},
		{"", ""},
		{"!!!", ""},
	}
	for _, c := range cases {
		if got := Normalize(c.in); got != c.want {
			t.Errorf("Normalize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// Two spellings of one title meet in the middle.
	same := [][2]string{
		{"Sherlock Jr.", "SHERLOCK JR"},
		{"Frédéric Chopin", "Frederic Chopin"},
		{"The Cabinet of Dr. Caligari", "Cabinet of Dr Caligari"},
		{"Tears of Steel", "tears.of.steel"},
	}
	for _, p := range same {
		if Normalize(p[0]) != Normalize(p[1]) {
			t.Errorf("Normalize(%q) = %q but Normalize(%q) = %q", p[0], Normalize(p[0]), p[1], Normalize(p[1]))
		}
	}
}

// ---- ParseMusic --------------------------------------------------------------

func TestParseMusic(t *testing.T) {
	cases := []struct {
		in   string
		want MusicRelease
	}{
		{"01 - Nocturne in E-flat major, Op. 9 No. 2.flac",
			MusicRelease{Format: "FLAC", Track: 1, Title: "Nocturne in E-flat major, Op. 9 No. 2"}},
		{"Frédéric Chopin/Nocturnes (2012) [FLAC]/CD1/01 - Nocturne in E-flat major, Op. 9 No. 2.flac",
			MusicRelease{Artist: "Frédéric Chopin", Album: "Nocturnes", Year: 2012, Format: "FLAC", Disc: 1, Track: 1, Title: "Nocturne in E-flat major, Op. 9 No. 2"}},
		{"Frédéric Chopin/Nocturnes (2012) [FLAC]/CD2/12 - Nocturne in C minor, Op. 48 No. 1.flac",
			MusicRelease{Artist: "Frédéric Chopin", Album: "Nocturnes", Year: 2012, Format: "FLAC", Disc: 2, Track: 12, Title: "Nocturne in C minor, Op. 48 No. 1"}},
		{`Frédéric Chopin\Waltzes (1998) [FLAC]\02 - Waltz in A minor, Op. 34 No. 2.flac`,
			MusicRelease{Artist: "Frédéric Chopin", Album: "Waltzes", Year: 1998, Format: "FLAC", Track: 2, Title: "Waltz in A minor, Op. 34 No. 2"}},
		{"Chopin/Préludes (1999) [MP3 320]/03 - Prélude No. 3 in G major.mp3",
			MusicRelease{Artist: "Chopin", Album: "Préludes", Year: 1999, Format: "MP3", Bitrate: "320kbps", Track: 3, Title: "Prélude No. 3 in G major"}},
		{"Chopin/Études - 1990 [24-96 FLAC]/01. Étude Op. 10 No. 1.flac",
			MusicRelease{Artist: "Chopin", Album: "Études", Year: 1990, Format: "FLAC", Bitrate: "24bit", Track: 1, Title: "Étude Op. 10 No. 1"}},
		{"Arthur Rubinstein - Chopin Waltzes (1963) [ALAC]/05 Waltz in A-flat major, Op. 42.m4a",
			MusicRelease{Artist: "Arthur Rubinstein", Album: "Chopin Waltzes", Year: 1963, Format: "ALAC", Track: 5, Title: "Waltz in A-flat major, Op. 42"}},
		{"Chopin/Ballades (2005)/01 Ballade No. 1 in G minor, Op. 23.flac",
			MusicRelease{Artist: "Chopin", Album: "Ballades", Year: 2005, Format: "FLAC", Track: 1, Title: "Ballade No. 1 in G minor, Op. 23"}},
		{"Chopin/Ballades/01 - Chopin - Ballade No. 4.flac",
			MusicRelease{Artist: "Chopin", Album: "Ballades", Format: "FLAC", Track: 1, Title: "Ballade No. 4"}},
		{"Various Artists/Piano Classics/01 - Frédéric Chopin - Nocturne Op. 9 No. 2.mp3",
			MusicRelease{Artist: "Various Artists", Album: "Piano Classics", Format: "MP3", Track: 1, Title: "Nocturne Op. 9 No. 2", TrackArtist: "Frédéric Chopin"}},
		{"Beethoven/Symphony No. 5 (1963) [MP3 V0]/03_Allegro.mp3",
			MusicRelease{Artist: "Beethoven", Album: "Symphony No. 5", Year: 1963, Format: "MP3", Bitrate: "V0", Track: 3, Title: "Allegro"}},
		{"Artist/Album (Deluxe Edition) (2019)/01 - Song.flac",
			MusicRelease{Artist: "Artist", Album: "Album (Deluxe Edition)", Year: 2019, Format: "FLAC", Track: 1, Title: "Song"}},
		{"Artist/2012 - Album/01 - Song.flac",
			MusicRelease{Artist: "Artist", Album: "Album", Year: 2012, Format: "FLAC", Track: 1, Title: "Song"}},
		{"Artist/Album - 2012/01 - Song.flac",
			MusicRelease{Artist: "Artist", Album: "Album", Year: 2012, Format: "FLAC", Track: 1, Title: "Song"}},
		{"Artist/Album [2012] [FLAC]/01 - Song.flac",
			MusicRelease{Artist: "Artist", Album: "Album", Year: 2012, Format: "FLAC", Track: 1, Title: "Song"}},
		{"Artist/Album/1-05 Song.flac",
			MusicRelease{Artist: "Artist", Album: "Album", Format: "FLAC", Disc: 1, Track: 5, Title: "Song"}},
		{"Artist/Album/CD2 - 03 - Song.flac",
			MusicRelease{Artist: "Artist", Album: "Album", Format: "FLAC", Disc: 2, Track: 3, Title: "Song"}},
		{"Artist/Album/Disc 2/05 - Song.flac",
			MusicRelease{Artist: "Artist", Album: "Album", Format: "FLAC", Disc: 2, Track: 5, Title: "Song"}},
		{"Artist/Album/CD1/101 Song.flac",
			MusicRelease{Artist: "Artist", Album: "Album", Format: "FLAC", Disc: 1, Track: 1, Title: "Song"}},
		{"Artist/Album/Artist - 03 - Song.flac",
			MusicRelease{Artist: "Artist", Album: "Album", Format: "FLAC", Track: 3, Title: "Song"}},
		{"Artist/Album [FLAC]/01.flac",
			MusicRelease{Artist: "Artist", Album: "Album", Format: "FLAC", Track: 1}},
		{"07.flac", MusicRelease{Format: "FLAC", Track: 7}},
		{"7 Rings.mp3", MusicRelease{Format: "MP3", Title: "7 Rings"}},
		{"10 Guitar Solo.flac", MusicRelease{Format: "FLAC", Track: 10, Title: "Guitar Solo"}},
		{"Chopin - Nocturne Op. 9 No. 2.flac", MusicRelease{Artist: "Chopin", Format: "FLAC", Title: "Nocturne Op. 9 No. 2"}},
		{"Nocturne.flac", MusicRelease{Format: "FLAC", Title: "Nocturne"}},
		{"Artist/Album/01 - Song.opus", MusicRelease{Artist: "Artist", Album: "Album", Format: "Opus", Track: 1, Title: "Song"}},
		{"Artist/Album/01 - Song.m4a", MusicRelease{Artist: "Artist", Album: "Album", Format: "AAC", Track: 1, Title: "Song"}},
		{"Artist/Album/01 - Song.ogg", MusicRelease{Artist: "Artist", Album: "Album", Format: "Vorbis", Track: 1, Title: "Song"}},
		{"Artist/Album (2001) [MP3 V2]/01 - Song.mp3", MusicRelease{Artist: "Artist", Album: "Album", Year: 2001, Format: "MP3", Bitrate: "V2", Track: 1, Title: "Song"}},
		{"Artist/Album (2001) (24bit)/01 - Song.flac", MusicRelease{Artist: "Artist", Album: "Album", Year: 2001, Format: "FLAC", Bitrate: "24bit", Track: 1, Title: "Song"}},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			if got := ParseMusic(c.in); got != c.want {
				t.Errorf("ParseMusic(%q)\n got  %+v\n want %+v", c.in, got, c.want)
			}
		})
	}
}

func TestMusicQualityName(t *testing.T) {
	for _, c := range []struct {
		m    MusicRelease
		want string
	}{
		{MusicRelease{Format: "FLAC"}, "FLAC"},
		{MusicRelease{Format: "FLAC", Bitrate: "24bit"}, "FLAC-24bit"},
		{MusicRelease{Format: "MP3", Bitrate: "320kbps"}, "MP3-320kbps"},
		{MusicRelease{}, "Unknown"},
	} {
		if got := c.m.QualityName(); got != c.want {
			t.Errorf("%+v.QualityName() = %q, want %q", c.m, got, c.want)
		}
	}
}

func BenchmarkParseRelease(b *testing.B) {
	names := []string{
		"Night.of.the.Living.Dead.1968.2160p.UHD.BluRay.REMUX.HDR10.HEVC.DTS-HD.MA.5.1-GRP.mkv",
		"Pioneer One (2010)/Season 01/Pioneer One - S01E02 - Earthfall.mkv",
		"Metropolis (1927).mkv",
	}
	for i := 0; i < b.N; i++ {
		_ = ParseRelease(names[i%len(names)])
	}
}
