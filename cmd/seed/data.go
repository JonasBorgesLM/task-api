package main

import "math/rand/v2"

// verbs and subjects combine into believable, varied task titles — e.g.
// "Buy groceries", "Fix the leaking faucet" — without a dependency (see
// CLAUDE.md: don't add one lightly). These are deliberately generic,
// everyday tasks: seed data for local development and manual testing, not
// a realistic domain dataset.
var verbs = []string{
	"Buy", "Fix", "Review", "Schedule", "Write", "Clean", "Renew", "Update",
	"Call", "Plan", "Organize", "Back up", "Test", "Deploy", "Refactor",
	"Document", "Research", "Cancel", "Book", "Return",
}

var subjects = []string{
	"groceries", "the leaking faucet", "the pull request", "a dentist appointment",
	"unit tests for the payment service", "the garage", "the passport",
	"project dependencies", "the plumber", "the team offsite",
	"the quarterly report", "the database backups", "the staging environment",
	"the onboarding docs", "the API rate limits", "the client meeting",
	"the car insurance", "the flight tickets", "the birthday gift",
	"the home office setup",
}

// descriptions includes several empty entries so a meaningful fraction of
// generated tasks have no description — Description is optional on the
// real API (see task.Service.CreateTask), and seed data should reflect
// that instead of always populating it.
var descriptions = []string{
	"",
	"",
	"",
	"Needs to be done before Friday.",
	"Low priority — whenever there's time.",
	"Follow up with the team before starting.",
	"Double-check with the manager first.",
	"Blocked until the previous step is done.",
	"Reminder from last week's meeting.",
	"High priority.",
	"Can be delegated if needed.",
}

// randomTask returns a randomly generated (title, description) pair,
// built by combining a random verb and subject — no title/description
// this produces ever approaches task.Service's length limits (200/2000
// characters), so seeding never fails validation.
func randomTask() (title, description string) {
	title = verbs[rand.IntN(len(verbs))] + " " + subjects[rand.IntN(len(subjects))]
	description = descriptions[rand.IntN(len(descriptions))]
	return title, description
}
