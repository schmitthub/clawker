package docker

import (
	"fmt"
	"math/rand"
	"regexp"
	"strings"
)

// validResourceNameRegex matches Docker's container/volume name rules:
// starts with alphanumeric, followed by alphanumeric, underscore, period, or hyphen.
var validResourceNameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// ValidateResourceName validates that a name is suitable for use in Docker
// resource names. Matches Docker CLI's container name rules:
// [a-zA-Z0-9][a-zA-Z0-9_.-]* with a max length of 128.
func ValidateResourceName(name string) error {
	if name == "" {
		return fmt.Errorf("name cannot be empty")
	}
	if len(name) > 128 {
		return fmt.Errorf("name is too long (%d characters, maximum 128)", len(name))
	}
	if !validResourceNameRegex.MatchString(name) {
		if strings.HasPrefix(name, "-") {
			return fmt.Errorf("invalid name %q: cannot start with a hyphen", name)
		}
		return fmt.Errorf("invalid name %q: only [a-zA-Z0-9][a-zA-Z0-9_.-] are allowed", name)
	}
	return nil
}

// NamePrefix is used for all clawker resource names.
const NamePrefix = "clawker"

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

// ImageTag generates image tag: clawker-project:latest
func ImageTag(project string) string {
	if project == "" {
		return NamePrefix + ":latest"
	}
	return fmt.Sprintf("%s-%s:latest", NamePrefix, project)
}

// ImageTagWithHash generates a content-addressed image tag: clawker-project:sha-<hash>
func ImageTagWithHash(project, hash string) string {
	if project == "" {
		return fmt.Sprintf("%s:sha-%s", NamePrefix, hash)
	}
	return fmt.Sprintf("%s-%s:sha-%s", NamePrefix, project, hash)
}

// NetworkName is the name of the clawker network.
const NetworkName = "clawker-net"

// GlobalVolumeName returns the name for a global (non-agent-scoped) volume.
// Example: GlobalVolumeName("globals") → "clawker-globals"
func GlobalVolumeName(purpose string) string {
	return fmt.Sprintf("%s-%s", NamePrefix, purpose)
}

// ParseContainerName extracts project and agent from container name.
// Container name format: clawker.project.agent
// Returns empty strings and false if the name doesn't match the format.
func ParseContainerName(name string) (project, agent string, ok bool) {
	// Remove leading slash if present (Docker adds it)
	name = strings.TrimPrefix(name, "/")
	parts := strings.Split(name, ".")
	switch {
	case len(parts) == 3 && parts[0] == NamePrefix:
		return parts[1], parts[2], true
	case len(parts) == 2 && parts[0] == NamePrefix:
		return "", parts[1], true
	default:
		return "", "", false
	}
}
