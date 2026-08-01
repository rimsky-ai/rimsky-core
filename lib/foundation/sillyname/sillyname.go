// Copyright © 2026 Fall Guy Consulting.
// SPDX-License-Identifier: AGPL-3.0-or-later OR LicenseRef-FallGuy-Commercial

// @concept: host-agent-proxy
// @concept: host-agent

package sillyname

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"regexp"
)

var adjectives = []string{
	"admiring", "adoring", "affectionate", "agitated", "amazing", "angry",
	"awesome", "beautiful", "blissful", "bold", "boring", "brave", "busy",
	"charming", "clever", "compassionate", "competent", "condescending",
	"confident", "cranky", "crazy", "dazzling", "determined", "distracted",
	"dreamy", "eager", "ecstatic", "elastic", "elated", "elegant",
	"eloquent", "epic", "exciting", "fervent", "festive", "flamboyant",
	"focused", "friendly", "frosty", "funny", "gallant", "gifted",
	"goofy", "gracious", "great", "happy", "hardcore", "heuristic",
	"hopeful", "hungry", "infallible", "inspiring", "intelligent",
	"interesting", "jolly", "jovial", "keen", "kind", "laughing",
	"loving", "lucid", "magical", "mystifying", "modest", "musing",
	"naughty", "nervous", "nice", "nifty", "nostalgic", "objective",
	"optimistic", "peaceful", "pedantic", "pensive", "practical",
	"priceless", "quirky", "quizzical", "recursing", "relaxed", "reverent",
	"romantic", "sad", "serene", "sharp", "silly", "sleepy", "sparkling",
	"stoic", "strange", "stupefied", "suspicious", "sweet", "tender",
	"thirsty", "trusting", "unruffled", "upbeat", "vibrant", "vigilant",
	"vigorous", "wizardly", "wonderful", "xenodochial", "youthful", "zealous",
	"zen",
}

var nouns = []string{
	"albatross", "antelope", "armadillo", "badger", "bat", "bear",
	"beaver", "bee", "bison", "buffalo", "butterfly", "camel", "capybara",
	"caribou", "cat", "cheetah", "chinchilla", "cobra", "cougar", "cow",
	"coyote", "crab", "crane", "crocodile", "crow", "deer", "dingo",
	"dolphin", "donkey", "dove", "dragonfly", "duck", "eagle", "eel",
	"elephant", "elk", "emu", "falcon", "ferret", "finch", "fox", "frog",
	"gazelle", "gecko", "gerbil", "giraffe", "goat", "goose", "gopher",
	"grasshopper", "hamster", "hare", "hawk", "hedgehog", "heron",
	"hippopotamus", "horse", "hummingbird", "hyena", "iguana", "impala",
	"jaguar", "jackal", "kangaroo", "kingfisher", "kiwi", "koala", "lark",
	"lemur", "leopard", "lion", "llama", "lobster", "lynx", "macaw",
	"magpie", "mammoth", "manatee", "mandrill", "mantis", "marmot",
	"meerkat", "mink", "mole", "mongoose", "monkey", "moose", "narwhal",
	"newt", "nightingale", "ocelot", "octopus", "okapi", "opossum",
	"orangutan", "orca", "ostrich", "otter", "owl", "ox", "panda",
	"panther", "parrot", "peacock", "pelican", "penguin", "pheasant",
	"pigeon", "platypus", "porcupine", "possum", "puffin", "quail",
	"quokka", "rabbit", "raccoon", "raven", "reindeer", "rhinoceros",
	"salamander", "salmon", "sandpiper", "seahorse", "seal", "shark",
	"sloth", "snail", "sparrow", "spider", "squid", "squirrel",
	"starfish", "stork", "swan", "tapir", "termite", "tiger", "toad",
	"tortoise", "toucan", "turtle", "unicorn", "vulture", "wallaby",
	"walrus", "wasp", "weasel", "whale", "wolf", "wolverine", "wombat",
	"woodpecker", "yak", "zebra",
}

var pattern = regexp.MustCompile(`^[a-z]+(?:-[a-z]+){1,3}$`)

const MaxLength = 64

// @concept: host-agent-proxy
// @concept: anonymous-mode
const AnonymousCredentialSentinel = "anonymous"

func Generate() string {
	return fmt.Sprintf("%s-%s", pick(adjectives), pick(nouns))
}

func pick(list []string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Errorf("sillyname: crypto/rand failure: %w", err))
	}
	n := binary.BigEndian.Uint64(b[:])
	return list[int(n%uint64(len(list)))]
}

func Validate(name string) error {
	if name == "" {
		return fmt.Errorf("routing label is empty")
	}
	if len(name) > MaxLength {
		return fmt.Errorf("routing label %q exceeds %d characters", name, MaxLength)
	}
	if !pattern.MatchString(name) {
		return fmt.Errorf("routing label %q must be lowercase kebab-case (two to four hyphen-separated ascii-letter words)", name)
	}
	return nil
}
