package loomchain

var (
	Version         = "2.0.0"
	BlueprintGitSHA = ""
	Build           = ""
	BuildVariant    = "generic"
	GitSHA          = ""
	GoLoomGitSHA    = ""
	EthGitSHA       = ""
	HashicorpGitSHA = ""
)

func FullVersion() string {
	lastPart := "b" + Build
	if Build == "" {
		lastPart = "dev"
		if GitSHA != "" {
			lastPart += "+" + GitSHA[:8]
		}
	}

	return Version + "+" + lastPart
}
