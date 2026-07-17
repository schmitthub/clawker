package docker

import (
	"fmt"
	"math/rand" // nosemgrep: go.lang.security.audit.crypto.math_random.math-random-used -- non-security random for Docker-style name generation
	"regexp"
	"strings"

	"github.com/schmitthub/clawker/internal/consts"
)

// validResourceNameRegex matches Docker's `RestrictedNameChars` rule
// for container, volume, and network names: starts with alphanumeric,
// followed by alphanumeric, underscore, period, or hyphen.
var validResourceNameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// ValidateResourceName validates that a name conforms to Docker's
// `RestrictedNameChars` pattern.
//
// Docker imposes NO engine-level length cap on container, volume, or
// network names. The 63-character DNS-label limit (RFC 1123 §2.1) only
// matters if Docker's name-based service discovery is in use on a
// user-defined network; clawker does not rely on container-name DNS
// resolution today, so no length cap is enforced here either.
//
// User-typed inputs (project/agent names) are normalized upstream by
// `cmdutil.ProjectSlugify` so this validator is mainly a friendly
// pre-flight for callers composing names without going through a
// Docker create; bypassing it just defers the same charset error to
// Docker's own create-time response.
func ValidateResourceName(name string) error {
	if name == "" {
		return fmt.Errorf("name cannot be empty")
	}
	if !validResourceNameRegex.MatchString(name) {
		if strings.HasPrefix(name, "-") {
			return fmt.Errorf("invalid name %q: cannot start with a hyphen", name)
		}
		return fmt.Errorf("invalid name %q: only [a-zA-Z0-9][a-zA-Z0-9_.-] are allowed", name)
	}
	return nil
}

// NamePrefix re-exports consts.NamePrefix as a docker-package alias so legacy
// callers in this package keep compiling. New code should reach for
// consts.NamePrefix directly.
const NamePrefix = consts.NamePrefix

// Adjectives for random name generation (Docker-style).
var adjectives = []string{
	"admiring", "adoring", "affectionate", "agitated", "amazing",
	"angry", "awesome", "beautiful", "blissful", "bold",
	"boring", "brave", "busy", "charming", "clever",
	"compassionate", "competent", "condescending", "confident", "cool",
	"cranky", "crazy", "dazzling", "determined", "distracted",
	"dreamy", "eager", "ecstatic", "elastic", "elated",
	"elegant", "eloquent", "epic", "exciting", "fervent",
	"festive", "flamboyant", "focused", "friendly", "frosty",
	"funny", "gallant", "gifted", "goofy", "gracious",
	"great", "happy", "hardcore", "heuristic", "hopeful",
	"hungry", "infallible", "inspiring", "intelligent", "interesting",
	"jolly", "jovial", "keen", "kind", "laughing",
	"loving", "lucid", "magical", "modest", "musing",
	"mystifying", "naughty", "nervous", "nice", "nifty",
	"nostalgic", "objective", "optimistic", "peaceful", "pedantic",
	"pensive", "practical", "priceless", "quirky", "quizzical",
	"recursing", "relaxed", "reverent", "romantic", "sad",
	"serene", "sharp", "silly", "sleepy", "stoic",
	"strange", "stupefied", "suspicious", "sweet", "tender",
	"thirsty", "trusting", "unruffled", "upbeat", "vibrant",
	"vigilant", "vigorous", "wizardly", "wonderful", "xenodochial",
	"youthful", "zealous", "zen",
}

// Nouns for random name generation (Docker-style).
var nouns = []string{
	"albattani", "allen", "almeida", "antonelli", "archimedes",
	"ardinghelli", "aryabhata", "austin", "babbage", "banach",
	"banzai", "bardeen", "bartik", "bassi", "beaver",
	"bell", "benz", "bhabha", "bhaskara", "blackburn",
	"blackwell", "bohr", "booth", "borg", "bose",
	"bouman", "boyd", "brahmagupta", "brattain", "brown",
	"buck", "burnell", "cannon", "carson", "cartwright",
	"carver", "cerf", "chandrasekhar", "chaplygin", "chatelet",
	"chatterjee", "chaum", "chebyshev", "clarke", "cohen",
	"colden", "cori", "cray", "curie", "curran",
	"darwin", "davinci", "dewdney", "diffie", "dijkstra",
	"dirac", "driscoll", "dubinsky", "easley", "edison",
	"einstein", "elbakyan", "elgamal", "elion", "ellis",
	"engelbart", "euclid", "euler", "faraday", "feistel",
	"fermat", "fermi", "feynman", "franklin", "gagarin",
	"galileo", "galois", "ganguly", "gates", "gauss",
	"germain", "goldberg", "goldstine", "goldwasser", "golick",
	"goodall", "gould", "greider", "grothendieck", "haibt",
	"hamilton", "haslett", "hawking", "heisenberg", "hellman",
	"hermann", "herschel", "hertz", "heyrovsky", "hodgkin",
	"hofstadter", "hoover", "hopper", "hugle", "hypatia",
	"ishizaka", "jackson", "jang", "jemison", "jennings",
	"jepsen", "johnson", "joliot", "jones", "kalam",
	"kapitsa", "kare", "keldysh", "keller", "kepler",
	"khayyam", "khorana", "kilby", "kirch", "knuth",
	"kowalevski", "lalande", "lamarr", "lamport", "leakey",
	"leavitt", "lederberg", "lehmann", "lewin", "lichterman",
	"liskov", "lovelace", "lumiere", "mahavira", "margulis",
	"matsumoto", "maxwell", "mayer", "mccarthy", "mcclintock",
	"mclaren", "mclean", "mcnulty", "meitner", "mendel",
	"mendeleev", "merkle", "mestorf", "mirzakhani", "montalcini",
	"moore", "morse", "moser", "murdock", "napier",
	"nash", "neumann", "newton", "nightingale", "nobel",
	"noether", "northcutt", "noyce", "panini", "pare",
	"pascal", "payne", "perlman", "pike", "poincare",
	"poitras", "proskuriakova", "ptolemy", "raman", "ramanujan",
	"rhodes", "ride", "ritchie", "roentgen", "rosalind",
	"rubin", "saha", "sammet", "sanderson", "satoshi",
	"shamir", "shannon", "shaw", "shockley", "shtern",
	"sinoussi", "snyder", "solomon", "spence", "stonebraker",
	"sutherland", "swanson", "swartz", "swirles", "taussig",
	"tesla", "tharp", "thompson", "torvalds", "turing",
	"varahamihira", "villani", "visvesvaraya", "volhard", "wescoff",
	"wilbur", "wiles", "williams", "williamson", "wilson",
	"wing", "wozniak", "wright", "wu", "yalow",
	"yonath", "zhukovsky",
}

// GenerateRandomName generates a Docker-style random name (adjective-noun).
func GenerateRandomName() string {
	adj := adjectives[rand.Intn(len(adjectives))]
	noun := nouns[rand.Intn(len(nouns))]
	return fmt.Sprintf("%s-%s", adj, noun)
}

// ContainerName generates container name: clawker.project.agent
// Returns an error if project or agent names contain invalid characters.
func ContainerName(project, agent string) (string, error) {
	if err := ValidateResourceName(agent); err != nil {
		return "", fmt.Errorf("invalid agent name: %w", err)
	}
	if project != "" {
		if err := ValidateResourceName(project); err != nil {
			return "", fmt.Errorf("invalid project name: %w", err)
		}
		return fmt.Sprintf("%s.%s.%s", NamePrefix, project, agent), nil
	}
	return fmt.Sprintf("%s.%s", NamePrefix, agent), nil
}

// ContainerNamesFromAgents resolves a slice of agent names to container names.
// If no agents are provided, returns the input slice unchanged.
// Returns an error if any agent or the project name is invalid.
func ContainerNamesFromAgents(project string, agents []string) ([]string, error) {
	if len(agents) == 0 {
		return agents, nil
	}
	containers := make([]string, len(agents))
	for i, agent := range agents {
		name, err := ContainerName(project, agent)
		if err != nil {
			return nil, err
		}
		containers[i] = name
	}
	return containers, nil
}

// ContainerNamePrefix returns prefix for filtering: clawker.project.
func ContainerNamePrefix(project string) string {
	if project == "" {
		return NamePrefix + "."
	}
	return fmt.Sprintf("%s.%s.", NamePrefix, project)
}

// VolumeName generates volume name: clawker.project.agent-purpose
// Returns an error if project or agent names contain invalid characters.
// The purpose parameter is not validated as it is always a hardcoded internal string.
//
// Volume-name purpose suffixes. VolumeName composes volume names as
// "clawker.<project>.<agent>-<purpose>". History and workspace are clawker
// infrastructure in the flat purpose namespace; harness-declared volumes and
// the clawker lifecycle volume are harness-scoped via HarnessVolumeName.
// VolumePurposes drives the removal fallback for unlabeled volumes and keeps
// the legacy pre-multi-harness flat "config" and "clawker" purposes so old
// agents still clean up fully.
const (
	VolumePurposeHistory   = consts.VolumePurposeHistory
	VolumePurposeWorkspace = consts.VolumePurposeWorkspace
	VolumePurposeClawker   = consts.VolumePurposeClawker
	// legacyVolumePurposeConfig is the pre-multi-harness config volume
	// suffix, retained for cleanup of volumes created by older versions.
	legacyVolumePurposeConfig = "config"
)

var VolumePurposes = []string{
	legacyVolumePurposeConfig,
	VolumePurposeHistory,
	VolumePurposeWorkspace,
	VolumePurposeClawker,
}

func VolumeName(project, agent, purpose string) (string, error) {
	if err := ValidateResourceName(agent); err != nil {
		return "", fmt.Errorf("invalid agent name: %w", err)
	}
	if project != "" {
		if err := ValidateResourceName(project); err != nil {
			return "", fmt.Errorf("invalid project name: %w", err)
		}
		return fmt.Sprintf("%s.%s.%s-%s", NamePrefix, project, agent, purpose), nil
	}
	return fmt.Sprintf("%s.%s-%s", NamePrefix, agent, purpose), nil
}

// HarnessVolumeName generates the name for a harness-scoped volume:
// clawker.<project>.<agent>-<harness>.<volume>. Bundle-declared persisted
// dirs and the clawker lifecycle volume are keyed by harness so two harnesses
// that declare the same volume name (both shipped harnesses declare "config")
// can never land on one another's state — the volume holds harness config
// AND the in-container login the user authenticated there.
//
// The harness segment is the harness's exact selection spelling: a bare
// floor/loose name, or the qualified namespace.bundle.component address for
// an installed-bundle harness (loadHarnessResolved puts that spelling in
// Bundle.Name). Segments join via consts.JoinIdentity — the shared
// address-separator helper — never a hardcoded separator.
//
// Injectivity — FOR A FIXED (project, agent) PAIR: the harness segment must
// satisfy consts.ValidateHarnessRef (bare = one dot-free token, qualified =
// exactly three dot-free tokens) and the volume segment consts.ValidateName
// (one dot-free token). The joined purpose therefore carries exactly one dot
// for a bare harness and exactly three for a qualified one, and splitting on
// dots recovers the pair — with project and agent fixed, no two (harness,
// volume) pairs can compose the same volume name. The bundle-load front door
// enforces the same volume-name rule (bundler.validateVolumeSpec);
// re-validating here makes that a local invariant of this function instead
// of a cross-package promise.
//
// The proof does NOT extend across agents: the agent segment joins the
// harness with "-", and agents (ValidateResourceName) and harness names
// (nameRe) both permit interior hyphens, so agent "dev" + harness "my-fork"
// composes the same name as agent "dev-my" + harness "fork". That aliasing
// forces equal names with DIFFERENT harness labels, so EnsureHarnessVolume's
// ownership check — not this composition — is what refuses it. (The same
// cross-agent ambiguity existed under the pre-harness flat scheme.)
func HarnessVolumeName(project, agent, harness, volume string) (string, error) {
	if err := consts.ValidateHarnessRef(harness); err != nil {
		return "", fmt.Errorf("invalid harness name: %w", err)
	}
	if err := consts.ValidateName(volume); err != nil {
		return "", fmt.Errorf("invalid harness volume name: %w", err)
	}
	return VolumeName(project, agent, consts.JoinIdentity(harness, volume))
}

// ImageTag generates the legacy image tag: clawker-project:latest. New
// builds tag by harness (HarnessImageTag); resolution still accepts :latest
// as the legacy fallback for images built before harness tags existed.
func ImageTag(project string) string {
	return imageRef(project, consts.ImageTagLatest)
}

// HarnessImageTag generates the harness-keyed image tag:
// clawker-<project>:<harness>. The tag IS the harness registry key.
func HarnessImageTag(project, harnessName string) string {
	return imageRef(project, harnessName)
}

// DefaultAliasImageTag is the alias tag applied to the default
// harness's image: clawker-<project>:default. Run/create resolve it when no
// explicit tag is selected.
func DefaultAliasImageTag(project string) string {
	return imageRef(project, consts.ImageTagDefaultAlias)
}

// BaseImageTag is the per-project shared base image tag:
// clawker-<project>:base. Harness images build FROM it; it is never
// runnable and never a harness selector.
func BaseImageTag(project string) string {
	return imageRef(project, consts.ImageTagBase)
}

func imageRef(project, tag string) string {
	if project == "" {
		return NamePrefix + ":" + tag
	}
	return fmt.Sprintf("%s-%s:%s", NamePrefix, project, tag)
}
