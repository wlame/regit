// internal/split/subcommits.go
package split

import (
	"time"

	"github.com/wlame/regit/internal/gitio"
	"github.com/wlame/regit/internal/model"
)

// BuildSubCommits turns ordered groups + their built trees into gitio.SubCommits.
// C1 inherits the original commit's dates; each subsequent sub-commit increments
// both author and committer dates by one second to stay strictly monotonic. WIP
// groups get the configured prefix on their subject.
func BuildSubCommits(c model.Commit, groups []model.HunkGroup, trees []string, wipPrefix string) []gitio.SubCommit {
	subs := make([]gitio.SubCommit, len(groups))
	for i, g := range groups {
		offset := time.Duration(i) * time.Second
		subject := g.Subject
		if g.WIP {
			subject = wipPrefix + subject
		}
		subs[i] = gitio.SubCommit{
			Tree:           trees[i],
			Subject:        subject,
			Body:           g.Body,
			AuthorName:     c.AuthorName,
			AuthorEmail:    c.AuthorEmail,
			CommitterName:  c.CommitterName,
			CommitterEmail: c.CommitterEmail,
			AuthorDate:     c.OrigAuthorDate.Add(offset),
			CommitDate:     c.OrigCommitDate.Add(offset),
		}
	}
	return subs
}
