package main

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Manik2708/gitcherrypick/backend/internal/port"
)

func TestEveryJobIsRunnableAndDescribed(t *testing.T) {
	// A job in the registry with no summary is invisible in --help, and one
	// with no runner is a name that fails only when someone tries it.
	require.NotEmpty(t, jobs)
	for name, j := range jobs {
		require.NotEmpty(t, j.summary, "job %q has no summary", name)
		require.NotNil(t, j.run, "job %q has no runner", name)
	}
}

func TestRegistryCoversPortJobService(t *testing.T) {
	// Every method on port.JobService must be registered. An unregistered one
	// is a job production silently stops running, and nothing else would say
	// so — the binary starts, the schedule runs, and the work never happens.
	//
	// Counted by REFLECTION rather than written down. A literal has to be
	// edited by whoever adds a method, which is exactly the person who just
	// forgot to register one.
	want := reflect.TypeOf((*port.JobService)(nil)).Elem().NumMethod()
	require.Len(t, jobs, want,
		"port.JobService has %d methods and %d are registered", want, len(jobs))

	for _, name := range []string{
		"expire-availability", "overdue-sweep", "expire-contact-requests",
		"expire-onboarding", "recompute-norms",
	} {
		require.Contains(t, jobs, name)
	}
}

func TestJobListIsSortedAndComplete(t *testing.T) {
	listed := jobList()
	for name := range jobs {
		require.Contains(t, listed, name)
	}
	require.Less(t, bytes.Index([]byte(listed), []byte("expire-availability")),
		bytes.Index([]byte(listed), []byte("overdue-sweep")),
		"listed alphabetically, so --help output is stable")
}

func TestListCommand(t *testing.T) {
	cmd := command()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"list"})

	require.NoError(t, cmd.Execute())
	require.Contains(t, out.String(), "overdue-sweep")
}

func TestRunRejectsAnUnknownJob(t *testing.T) {
	err := runJob(context.Background(), config{databaseURL: "postgres://x"}, "nonsense")
	require.ErrorContains(t, err, `unknown job "nonsense"`)

	// The available jobs are listed, so an operator who typos does not have to
	// go find the source.
	require.ErrorContains(t, err, "overdue-sweep")
}

func TestRunRequiresADatabase(t *testing.T) {
	err := runJob(context.Background(), config{}, "overdue-sweep")
	require.ErrorContains(t, err, "--database-url")
}

func TestRunReportsAnUnreachableClockSource(t *testing.T) {
	// A clock URL that was passed and does not work is a misconfiguration, and
	// the job must refuse rather than run against the wrong time (ADR-0012).
	err := runJob(context.Background(), config{
		databaseURL: "postgres://user:pass@127.0.0.1:1/db",
		clockURL:    "http://127.0.0.1:1/_clock",
	}, "overdue-sweep")
	require.Error(t, err)
}

func TestFlagDefaults(t *testing.T) {
	cmd := command()

	url, err := cmd.PersistentFlags().GetString("clock-url")
	require.NoError(t, err)
	require.Empty(t, url, "production default is the system clock")

	resendURL, err := cmd.PersistentFlags().GetString("resend-api-url")
	require.NoError(t, err)
	require.Equal(t, "https://api.resend.com", resendURL, "the default is the real host")
}
