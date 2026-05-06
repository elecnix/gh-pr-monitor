package await

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCountUnresolvedThreads(t *testing.T) {
	tests := []struct {
		name     string
		threads  []ReviewThread
		expected int
	}{
		{
			name:     "empty threads",
			threads:  []ReviewThread{},
			expected: 0,
		},
		{
			name: "all resolved",
			threads: []ReviewThread{
				{IsResolved: true},
				{IsResolved: true},
			},
			expected: 0,
		},
		{
			name: "some unresolved",
			threads: []ReviewThread{
				{IsResolved: true},
				{IsResolved: false},
				{IsResolved: false},
			},
			expected: 2,
		},
		{
			name: "none resolved",
			threads: []ReviewThread{
				{IsResolved: false},
				{IsResolved: false},
			},
			expected: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr := &PullRequest{
				ReviewThreads: ThreadNodes{Nodes: tt.threads},
			}
			assert.Equal(t, tt.expected, CountUnresolvedThreads(pr))
		})
	}
}

func TestHasConflicts(t *testing.T) {
	tests := []struct {
		name      string
		mergeable string
		expected  bool
	}{
		{"mergeable", "MERGEABLE", false},
		{"clean", "CLEAN", false},
		{"conflicting", "CONFLICTING", true},
		{"unknown", "UNKNOWN", false},
		{"", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr := &PullRequest{Mergeable: tt.mergeable}
			assert.Equal(t, tt.expected, HasConflicts(pr))
		})
	}
}

func TestFailingChecks(t *testing.T) {
	t.Run("no suites", func(t *testing.T) {
		pr := &PullRequest{Commits: CommitNodes{Nodes: []Commit{{}}}}
		assert.Empty(t, FailingChecks(pr))
	})

	t.Run("failure conclusion", func(t *testing.T) {
		pr := &PullRequest{
			Commits: CommitNodes{
				Nodes: []Commit{
					{
						Commit: CommitDetails{
							CheckSuites: SuiteNodes{
								Nodes: []CheckSuite{
									{Conclusion: "FAILURE", App: AppInfo{Name: "CI"}},
								},
							},
						},
					},
				},
			},
		}
		assert.Equal(t, []string{"CI"}, FailingChecks(pr))
	})

	t.Run("error conclusion", func(t *testing.T) {
		pr := &PullRequest{
			Commits: CommitNodes{
				Nodes: []Commit{
					{
						Commit: CommitDetails{
							CheckSuites: SuiteNodes{
								Nodes: []CheckSuite{
									{Conclusion: "ERROR", App: AppInfo{Name: "Build"}},
								},
							},
						},
					},
				},
			},
		}
		assert.Equal(t, []string{"Build"}, FailingChecks(pr))
	})

	t.Run("success conclusion", func(t *testing.T) {
		pr := &PullRequest{
			Commits: CommitNodes{
				Nodes: []Commit{
					{
						Commit: CommitDetails{
							CheckSuites: SuiteNodes{
								Nodes: []CheckSuite{
									{Conclusion: "SUCCESS", App: AppInfo{Name: "CI"}},
								},
							},
						},
					},
				},
			},
		}
		assert.Empty(t, FailingChecks(pr))
	})

	t.Run("failing check run", func(t *testing.T) {
		pr := &PullRequest{
			Commits: CommitNodes{
				Nodes: []Commit{
					{
						Commit: CommitDetails{
							CheckSuites: SuiteNodes{
								Nodes: []CheckSuite{
									{
										Conclusion: "SUCCESS",
										CheckRuns: RunNodes{
											Nodes: []CheckRun{
												{Name: "test", Conclusion: "FAILURE"},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}
		assert.Equal(t, []string{"test"}, FailingChecks(pr))
	})
}

func TestPendingChecks(t *testing.T) {
	t.Run("no suites", func(t *testing.T) {
		pr := &PullRequest{Commits: CommitNodes{Nodes: []Commit{{}}}}
		assert.Empty(t, PendingChecks(pr))
	})

	t.Run("in_progress status", func(t *testing.T) {
		pr := &PullRequest{
			Commits: CommitNodes{
				Nodes: []Commit{
					{
						Commit: CommitDetails{
							CheckSuites: SuiteNodes{
								Nodes: []CheckSuite{
									{Status: "IN_PROGRESS", App: AppInfo{Name: "CI"}},
								},
							},
						},
					},
				},
			},
		}
		assert.Equal(t, []string{"CI"}, PendingChecks(pr))
	})

	t.Run("queued status", func(t *testing.T) {
		pr := &PullRequest{
			Commits: CommitNodes{
				Nodes: []Commit{
					{
						Commit: CommitDetails{
							CheckSuites: SuiteNodes{
								Nodes: []CheckSuite{
									{Status: "QUEUED", App: AppInfo{Name: "Build"}},
								},
							},
						},
					},
				},
			},
		}
		assert.Equal(t, []string{"Build"}, PendingChecks(pr))
	})

	t.Run("completed status", func(t *testing.T) {
		pr := &PullRequest{
			Commits: CommitNodes{
				Nodes: []Commit{
					{
						Commit: CommitDetails{
							CheckSuites: SuiteNodes{
								Nodes: []CheckSuite{
									{Status: "COMPLETED", App: AppInfo{Name: "CI"}},
								},
							},
						},
					},
				},
			},
		}
		assert.Empty(t, PendingChecks(pr))
	})
}

func TestParseMode(t *testing.T) {
	tests := []struct {
		input    string
		expected Mode
		err      bool
	}{
		{"all", ModeAll, false},
		{"comments", ModeComments, false},
		{"conflicts", ModeConflicts, false},
		{"actions", ModeActions, false},
		{"ALL", ModeAll, false},
		{"Comments", ModeComments, false},
		{"invalid", "", true},
		{"", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			mode, err := ParseMode(tt.input)
			if tt.err {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, mode)
			}
		})
	}
}

func TestConditions(t *testing.T) {
	t.Run("all mode - clean PR", func(t *testing.T) {
		pr := &PullRequest{
			Mergeable: "MERGEABLE",
			ReviewThreads: ThreadNodes{
				Nodes: []ReviewThread{
					{IsResolved: true},
				},
			},
			Commits: CommitNodes{
				Nodes: []Commit{
					{
						Commit: CommitDetails{
							CheckSuites: SuiteNodes{
								Nodes: []CheckSuite{
									{Conclusion: "SUCCESS"},
								},
							},
						},
					},
				},
			},
		}
		assert.Empty(t, Conditions(pr, ModeAll))
	})

	t.Run("all mode - has unresolved", func(t *testing.T) {
		pr := &PullRequest{
			Mergeable: "MERGEABLE",
			ReviewThreads: ThreadNodes{
				Nodes: []ReviewThread{
					{IsResolved: false},
				},
			},
		}
		conds := Conditions(pr, ModeAll)
		assert.Contains(t, conds, "unresolved-threads")
	})

	t.Run("all mode - has conflicts", func(t *testing.T) {
		pr := &PullRequest{Mergeable: "CONFLICTING"}
		conds := Conditions(pr, ModeAll)
		assert.Contains(t, conds, "conflicts")
	})

	t.Run("all mode - has failing checks", func(t *testing.T) {
		pr := &PullRequest{
			Commits: CommitNodes{
				Nodes: []Commit{
					{
						Commit: CommitDetails{
							CheckSuites: SuiteNodes{
								Nodes: []CheckSuite{
									{Conclusion: "FAILURE"},
								},
							},
						},
					},
				},
			},
		}
		conds := Conditions(pr, ModeAll)
		assert.Contains(t, conds, "actions:failing")
	})

	t.Run("comments mode only", func(t *testing.T) {
		pr := &PullRequest{
			Mergeable: "CONFLICTING",
			ReviewThreads: ThreadNodes{
				Nodes: []ReviewThread{
					{IsResolved: false},
				},
			},
		}
		conds := Conditions(pr, ModeComments)
		assert.Contains(t, conds, "unresolved-threads")
		assert.NotContains(t, conds, "conflicts")
	})

	t.Run("conflicts mode only", func(t *testing.T) {
		pr := &PullRequest{
			Mergeable: "CONFLICTING",
			ReviewThreads: ThreadNodes{
				Nodes: []ReviewThread{
					{IsResolved: false},
				},
			},
		}
		conds := Conditions(pr, ModeConflicts)
		assert.Contains(t, conds, "conflicts")
		assert.NotContains(t, conds, "unresolved-threads")
	})
}

func TestSecondsToHuman(t *testing.T) {
	tests := []struct {
		seconds  int
		expected string
	}{
		{30, "30 second(s)"},
		{60, "1 minute(s)"},
		{120, "2 minute(s)"},
		{3600, "1 hour(s)"},
		{7200, "2 hour(s)"},
		{86400, "1 day(s)"},
		{172800, "2 day(s)"},
		{90, "1 minute(s)"},
		{3661, "1 hour(s)"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			assert.Equal(t, tt.expected, SecondsToHuman(tt.seconds))
		})
	}
}

func TestSuiteName(t *testing.T) {
	t.Run("uses app name", func(t *testing.T) {
		suite := &CheckSuite{App: AppInfo{Name: "GitHub Actions", Slug: "github-actions"}}
		assert.Equal(t, "GitHub Actions", suiteName(suite))
	})

	t.Run("falls back to slug", func(t *testing.T) {
		suite := &CheckSuite{App: AppInfo{Name: "", Slug: "github-actions"}}
		assert.Equal(t, "github-actions", suiteName(suite))
	})

	t.Run("empty", func(t *testing.T) {
		suite := &CheckSuite{App: AppInfo{Name: "", Slug: ""}}
		assert.Equal(t, "", suiteName(suite))
	})
}

func TestFailingAnnotations(t *testing.T) {
	t.Run("no failing checks", func(t *testing.T) {
		pr := &PullRequest{Commits: CommitNodes{Nodes: []Commit{{}}}}
		assert.Empty(t, FailingAnnotations(pr))
	})

	t.Run("failing check run with annotations", func(t *testing.T) {
		line3 := 3
		line5 := 5
		pr := &PullRequest{
			Commits: CommitNodes{
				Nodes: []Commit{
					{
						Commit: CommitDetails{
							CheckSuites: SuiteNodes{
								Nodes: []CheckSuite{
									{
										Conclusion: "SUCCESS",
										CheckRuns: RunNodes{
											Nodes: []CheckRun{
												{
													Name:       "lint",
													Conclusion: "FAILURE",
													Annotations: AnnotationNodes{
														Nodes: []CheckAnnotation{
															{
																AnnotationLevel: "FAILURE",
																Message:         "unused variable",
																Path:            "main.go",
																StartLine:       &line3,
																EndLine:         &line5,
																Title:           "no-unused-vars",
															},
														},
														TotalCount: 1,
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}
		annotations := FailingAnnotations(pr)
		assert.Len(t, annotations, 1)
		assert.Equal(t, "FAILURE", annotations[0].AnnotationLevel)
		assert.Equal(t, "unused variable", annotations[0].Message)
		assert.Equal(t, "main.go", annotations[0].Path)
		assert.Equal(t, 3, *annotations[0].StartLine)
		assert.Equal(t, 5, *annotations[0].EndLine)
		assert.Equal(t, "no-unused-vars", annotations[0].Title)
	})

	t.Run("successful check run excludes annotations", func(t *testing.T) {
		line1 := 1
		pr := &PullRequest{
			Commits: CommitNodes{
				Nodes: []Commit{
					{
						Commit: CommitDetails{
							CheckSuites: SuiteNodes{
								Nodes: []CheckSuite{
									{
										Conclusion: "SUCCESS",
										CheckRuns: RunNodes{
											Nodes: []CheckRun{
												{
													Name:       "build",
													Conclusion: "SUCCESS",
													Annotations: AnnotationNodes{
														Nodes: []CheckAnnotation{
															{
																AnnotationLevel: "NOTICE",
																Message:         "info only",
																Path:            "main.go",
																StartLine:       &line1,
																EndLine:         &line1,
																Title:           "info",
															},
														},
														TotalCount: 1,
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}
		assert.Empty(t, FailingAnnotations(pr))
	})

	t.Run("multiple failing check runs", func(t *testing.T) {
		line10 := 10
		line20 := 20
		pr := &PullRequest{
			Commits: CommitNodes{
				Nodes: []Commit{
					{
						Commit: CommitDetails{
							CheckSuites: SuiteNodes{
								Nodes: []CheckSuite{
									{
										CheckRuns: RunNodes{
											Nodes: []CheckRun{
												{
													Name:       "lint",
													Conclusion: "FAILURE",
													Annotations: AnnotationNodes{
														Nodes: []CheckAnnotation{
															{AnnotationLevel: "WARNING", Message: "w1", Path: "a.go", StartLine: &line10, EndLine: &line10, Title: "t1"},
														},
														TotalCount: 1,
													},
												},
												{
													Name:       "test",
													Conclusion: "ERROR",
													Annotations: AnnotationNodes{
														Nodes: []CheckAnnotation{
															{AnnotationLevel: "FAILURE", Message: "f1", Path: "b.go", StartLine: &line20, EndLine: &line20, Title: "t2"},
														},
														TotalCount: 1,
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}
		annotations := FailingAnnotations(pr)
		assert.Len(t, annotations, 2)
		assert.Equal(t, "w1", annotations[0].Message)
		assert.Equal(t, "f1", annotations[1].Message)
	})

	t.Run("nil start/end line", func(t *testing.T) {
		pr := &PullRequest{
			Commits: CommitNodes{
				Nodes: []Commit{
					{
						Commit: CommitDetails{
							CheckSuites: SuiteNodes{
								Nodes: []CheckSuite{
									{
										CheckRuns: RunNodes{
											Nodes: []CheckRun{
												{
													Name:       "sonar",
													Conclusion: "FAILURE",
													Annotations: AnnotationNodes{
														Nodes: []CheckAnnotation{
															{
																AnnotationLevel: "FAILURE",
																Message:         "bug found",
																Path:            "app.js",
																StartLine:       nil,
																EndLine:          nil,
																Title:           "Bug",
															},
														},
														TotalCount: 1,
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		}
		annotations := FailingAnnotations(pr)
		assert.Len(t, annotations, 1)
		assert.Nil(t, annotations[0].StartLine)
		assert.Nil(t, annotations[0].EndLine)
	})
}
