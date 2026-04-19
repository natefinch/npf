+++
title = "Optimizing for Reviewers: The Three Step AI Dev Loop"
date = 2026-04-19T12:01:33-04:00
type = "post"
+++


With AI writing a lot more of our code, we need to optimize for the human code reviewer.

The key to this is good test coverage and minimizing diffs in changes that humans need to verify. 

We should strive to separate refactors that don't change logic from changes that intentionally change logic (bug fixes, feature additions, etc). Separating the two makes it significantly easier to understand if a change is correct. This was always best practice, but it was often burdensome because of the additional human effort required. Now with AI to do the boring parts, it's more practical to put into practice.

With broad enough test coverage, you can refactor the code all you want and still ensure it behaves the same. Then the code reviewer only needs to review the general shape of the new code to know if it'll be fit for upcoming features.

When adding new features, minimize the diff in production code as much as possible, even if the code is suboptimal. This makes it easier for reviewers to understand if the change is correct or not. You can always have the AI refactor it later.

The best way to optimize for reviews is to break changes into three steps, each in separate PRs / commits that can be reviewed and verified independently.

## The Three Step AI Dev Loop

1. Ensure tests validate current behavior, add tests if needed.
  - Change the production code as little as possible here.
  - Write your tests to be as broadly as possible, to make it easier to refactor later. 

2. Refactor the code so that it's easier to add the feature you want.
  - Refactor as much as you can without changing tests at all, or with minimal, easy to verify changes.
  - If you need to change the tests significantly to support a refactor, go back to #1.

3. Write the feature/bugfix. 
  - Have the AI write the failing test for the new code first.
  - Have tha AI then implement the feature/bugfix in a way that minimizes the diff from the old production code.

For #1, You want as close to zero production code changes as possible so that you know you're just validating what already exists. You want tests that are likely to survive a refactor, or that will only need to change in minimal, easily verifiable ways.

For #2 we refactor to make the code cleaer, more maintainable, more performant, and/or easier for the AI and humans to understand. It's imporant not to change the tests so that humans know the behavior of the production code hasn't changed. If small changes are needed and can be done in a way that is easy for AI and humans to verify, that's fine (replacing a concrete type with an interface, for example).

For #3, this is really just TDD, but the point is to minimize the diff, even to the point of suboptimal code with respect to performance, maintainability, etc. Since we're changing behavior, we have to change both the tests and the production code at the same time, and this is where bugs can slip in. So we optimize for changes that are *most obviously correct* beyond all other metrics. You want simple, straight-forward changes that are easy for a human to verify. 

After #3, you begin the loop again, and you can use #1 and #2 to refine the code from #3 to be actually-good code. Since the human knows the simple change in #3 was correct, and the tests fully constrain behavior, we can then refactor the obviously-correct code into more performant, more maintainable, more resilient code without worrying if the actual behavior is correct.

## Conclusion

The key is, 
- When you change prod, don't change the tests. 
- When you change the tests, don't change prod. 
- Any time you change both, minimize the diff and refactor later.