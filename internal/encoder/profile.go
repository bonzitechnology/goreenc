package encoder

import "fmt"

// Profile represents an encoding profile based on resolution
type Profile struct {
	Name      string
	CRF       int
	Preset    string
	X265Params []string
}

// GetProfile returns the appropriate encoding profile based on height and codec
func GetProfile(height int, useAV1 bool) Profile {
	if useAV1 {
		// svt-av1 uses a 0-63 CRF scale and a numeric preset (-2 to 13);
		// x265-style presets like "medium" are rejected
		name := "1080p+"
		if height < 720 {
			name = "< 720p"
		} else if height < 1080 {
			name = "720p"
		}
		return Profile{
			Name:   name,
			CRF:    30,
			Preset: "6",
		}
	}

	var profile Profile

	if height < 720 {
		profile = Profile{
			Name:   "< 720p",
			CRF:    20,
			Preset: "medium",
			X265Params: []string{
				"sao=0",
				"aq-mode=3",
				"max-merge=4",
			},
		}
	} else if height < 1080 {
		profile = Profile{
			Name:   "720p",
			CRF:    20,
			Preset: "medium",
			X265Params: []string{
				"sao=0",
				"aq-mode=3",
				"max-merge=4",
			},
		}
	} else {
		profile = Profile{
			Name:   "1080p+",
			CRF:    22,
			Preset: "medium",
			X265Params: []string{
				"sao=0",
				"max-merge=4",
			},
		}
	}

	return profile
}

// GetX265ParamsString returns x265 params as a colon-separated string.
// Every param must be key=value: ffmpeg mis-parses bare flags like "no-sao",
// which silently drops that param and the one after it.
func (p *Profile) GetX265ParamsString() string {
	result := ""
	for i, param := range p.X265Params {
		if i > 0 {
			result += ":"
		}
		result += param
	}
	return result
}

// String returns a human-readable profile description
func (p *Profile) String() string {
	return fmt.Sprintf("%s (CRF %d, %s)", p.Name, p.CRF, p.Preset)
}
