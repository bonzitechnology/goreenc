package encoder

import "fmt"

// Profile represents an encoding profile based on resolution
type Profile struct {
	Name      string
	CRF       int
	Preset    string
	X265Params []string
}

// GetProfile returns the appropriate encoding profile based on height
func GetProfile(height int) Profile {
	var profile Profile

	if height < 720 {
		profile = Profile{
			Name:   "< 720p",
			CRF:    20,
			Preset: "medium",
			X265Params: []string{
				"pools=none",
				"no-sao",
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
				"pools=none",
				"no-sao",
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
				"pools=none",
				"no-sao",
				"max-merge=4",
			},
		}
	}

	return profile
}

// GetX265ParamsString returns x265 params as a colon-separated string
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
