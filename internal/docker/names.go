package docker

import (
	"fmt"
	"math/rand"
	"strings"
)

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
func ContainerName(project, agent string) string {
	return fmt.Sprintf("%s.%s.%s", NamePrefix, project, agent)
}

// ContainerNamePrefix returns prefix for filtering: clawker.project.
func ContainerNamePrefix(project string) string {
	return fmt.Sprintf("%s.%s.", NamePrefix, project)
}

// VolumeName generates volume name: clawker.project.agent-purpose
func VolumeName(project, agent, purpose string) string {
	return fmt.Sprintf("%s.%s.%s-%s", NamePrefix, project, agent, purpose)
}

// ImageTag generates image tag: clawker-project:latest
func ImageTag(project string) string {
	return fmt.Sprintf("%s-%s:latest", NamePrefix, project)
}

// NetworkName is the name of the clawker network.
const NetworkName = "clawker-net"

// ParseContainerName extracts project and agent from container name.
// Container name format: clawker.project.agent
// Returns empty strings and false if the name doesn't match the format.
func ParseContainerName(name string) (project, agent string, ok bool) {
	// Remove leading slash if present (Docker adds it)
	name = strings.TrimPrefix(name, "/")
	parts := strings.Split(name, ".")
	if len(parts) != 3 || parts[0] != NamePrefix {
		return "", "", false
	}
	return parts[1], parts[2], true
}

// IsAlpineImage checks if an image reference appears to be Alpine-based.
func IsAlpineImage(imageRef string) bool {
	imageRef = strings.ToLower(imageRef)
	return strings.Contains(imageRef, "alpine")
}
