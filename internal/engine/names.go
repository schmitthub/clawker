package engine

import (
	"fmt"
	"math/rand"
	"strings"
)

// Adjectives for random name generation (Docker-style)
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

// Nouns for random name generation (Docker-style)
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

// GenerateRandomName generates a Docker-style random name (adjective-noun)
func GenerateRandomName() string {
	adj := adjectives[rand.Intn(len(adjectives))]
	noun := nouns[rand.Intn(len(nouns))]
	return fmt.Sprintf("%s-%s", adj, noun)
}

// ParseContainerName extracts project and agent from container name
// Container name format: claucker/project/agent
func ParseContainerName(name string) (project, agent string, ok bool) {
	// Remove leading slash if present (Docker adds it)
	name = strings.TrimPrefix(name, "/")
	parts := strings.Split(name, "/")
	if len(parts) != 3 || parts[0] != "claucker" {
		return "", "", false
	}
	return parts[1], parts[2], true
}

// ContainerName generates container name: claucker/project/agent
func ContainerName(projectName, agentName string) string {
	return fmt.Sprintf("claucker/%s/%s", projectName, agentName)
}

// ContainerNamePrefix returns prefix for filtering: claucker/project/
func ContainerNamePrefix(projectName string) string {
	return fmt.Sprintf("claucker/%s/", projectName)
}

// VolumeName generates volume name: claucker/project/agent-purpose
func VolumeName(projectName, agentName, purpose string) string {
	return fmt.Sprintf("claucker/%s/%s-%s", projectName, agentName, purpose)
}
