package main

import (
	"math/rand/v2"

	"github.com/JonasBorgesLM/task-api/internal/task"
)

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

// statusWeights gives each task.Status a roughly realistic share of seeded
// tasks: mostly still active (pending/in_progress), a healthy fraction
// actually finished, and cancelled kept rare — reflecting how an
// established task list tends to look, rather than a uniform 25% each.
var statusWeights = []struct {
	status task.Status
	weight float64
}{
	{task.StatusPending, 0.35},
	{task.StatusInProgress, 0.25},
	{task.StatusDone, 0.30},
	{task.StatusCancelled, 0.10},
}

// randomStatus picks a task.Status according to statusWeights. The caller
// creates every task as task.StatusPending (task.Service.CreateTask's only
// option) and then, if this returns anything else, moves it there with
// exactly one TransitionStatus call — every non-pending value here is
// reachable in a single hop from pending (see Service's legalTransitions
// table), so no multi-step transition sequence is ever needed.
func randomStatus() task.Status {
	roll := rand.Float64()
	var cumulative float64
	for _, sw := range statusWeights {
		cumulative += sw.weight
		if roll < cumulative {
			return sw.status
		}
	}
	return task.StatusPending // unreachable unless statusWeights' weights don't sum to 1
}

// randomPriority picks uniformly among the three Priority values — unlike
// status, there's no realistic skew to model here.
func randomPriority() task.Priority {
	priorities := []task.Priority{task.PriorityLow, task.PriorityMedium, task.PriorityHigh}
	return priorities[rand.IntN(len(priorities))]
}
