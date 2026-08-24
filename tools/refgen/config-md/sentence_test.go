package main

import "testing"

// firstSentence decides what the configuration reference publishes for every
// field. It used to break at the FIRST ". ", so any abbreviation cut the
// description mid-phrase — "pins the BNK release, e.g." stopped immediately
// before the only part that mattered, and fourteen rows shipped that way. The
// undocumented-field ratchet cannot catch it: those rows are not blank.
func TestFirstSentence(t *testing.T) {
	cases := []struct{ in, want string }{
		// the regression that prompted this
		{`ManifestVersion pins the BNK release, e.g. "2.4.0-EA". This is the single field that selects the line.`,
			`ManifestVersion pins the BNK release, e.g. "2.4.0-EA".`},
		// parenthesised abbreviation — the form six rows used
		{`ICRHost overrides the host (e.g. "de.icr.io"). Empty derives it.`,
			`ICRHost overrides the host (e.g. "de.icr.io").`},
		{`Use it i.e. always. Second sentence.`, `Use it i.e. always.`},
		{`Charts, images, etc. are mirrored. Second sentence.`, `Charts, images, etc. are mirrored.`},
		{`Local vs. remote clients. Second sentence.`, `Local vs. remote clients.`},
		// a genuine sentence boundary must still split
		{`First sentence here. Second sentence.`, `First sentence here.`},
		// single trailing sentence gets its period
		{`Only one sentence`, `Only one sentence.`},
		{`Only one sentence.`, `Only one sentence.`},
		// a lone initial is not a sentence end
		{`Named after F. Smith and others. Second.`, `Named after F. Smith and others.`},
		{``, ``},
	}
	for _, c := range cases {
		if got := firstSentence(c.in); got != c.want {
			t.Errorf("firstSentence(%q)\n got: %q\nwant: %q", c.in, got, c.want)
		}
	}
}
