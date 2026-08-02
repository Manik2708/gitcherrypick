# Calibration — finding out whether any of this is right

Every constant in [rubric-v1.md](rubric-v1.md) is reasoned, not measured. Nothing here
establishes that a claim scoring 82 is better than one scoring 74. This file is the plan for
finding out, and it is **not yet actionable** — it needs claims, and there are none.

## What "right" means

Not that scores match some true value — there isn't one. The bar is weaker and testable:

**Ordering.** Given two claims a knowledgeable engineer would rank confidently, the rubric
ranks them the same way. Absolute values can be off by ten points without anyone caring;
inversions are fatal.

**Stability.** The same claim scored twice returns close to the same number. Adaptive
thinking makes runs non-identical; the dimensions are coarse enough that ±3 should be
typical and ±10 is a defect.

**Spread.** Scores use the range. If everything lands between 68 and 79, the rubric has
measured nothing regardless of how correctly ordered it is.

**Explicability.** A contributor reading the rationale agrees it describes their work, even
when they disagree with the number. This is the one users will actually judge us on.

## The blocker

Validation needs claims a human has independently ranked. Claims cannot exist before the
platform does, and a hand-built set has the problem that whoever builds it is also the
person whose judgement the rubric is being fitted to.

Three options, in the order they become available:

**1 · Seed set, built by the owner (pre-launch).** Assemble 40–60 real merged PRs across a
few skills, rank them by hand, and check the rubric agrees. Fast and free. Encodes one
person's judgement as ground truth — which for a v1 product is arguably correct, since it is
your product and your definition of a good engineer is the one being sold.

**2 · Small panel (pre-launch, if people are available).** Three to five experienced
engineers independently rank the same set. The valuable output is not the ranking but the
**disagreement**: wherever raters diverge, the rubric is ambiguous and the definition needs
tightening. Costs other people's time.

**3 · Real claims (post-launch).** Once claims exist, sample and rank them. The only source
that reflects what contributors actually submit rather than what we imagined they would.

## Procedure

1. **Fix the set before scoring anything.** Choosing examples after seeing scores fits the
   set to the rubric rather than the reverse.
2. **Rank pairwise, not absolutely.** "Is A better than B for this skill?" is a question
   humans answer consistently. "Score A out of 100" is not.
3. **Score with the rubric, at effort `high`, twice per claim** — once for ordering, once
   for stability.
4. **Measure:** rank correlation against the human ordering; inversion count and where;
   score standard deviation across the set; run-to-run variance.
5. **Adjust one constant at a time**, re-run, keep what improves ordering. Changing several
   at once means learning nothing about any of them.
6. **Record every run** — set version, rubric version, model, results. A tuning history
   nobody wrote down is a tuning history that gets repeated.

## What to look for first

| Symptom | Likely cause |
|---|---|
| Everything scores 65–80 | Model avoiding the ends of the scale. Tighten the anchors; the archetypes are not distinct enough. |
| Large PRs consistently outrank small ones | `substance` is reading diff size despite the exclusion. |
| Popular-repo PRs dominate regardless of quality | `w_r = 0.20` is too high, or `R` needs a steeper curve. |
| Dimensions move together | Halo scoring — the model is forming one impression and spreading it. The most likely failure mode. Consider scoring dimensions in separate passes. |
| Generalists rank far below specialists | `d` or `γ` too aggressive. |
| Two runs differ by 10+ | Anchors ambiguous, or `max_tokens` truncating before the model finishes. |

## Launch posture

**Not decided — this needs the owner.** Three options:

| Option | Trade |
|---|---|
| **Ship, don't rank until tuned** | Contributors claim skills and see their own scores; hirer search and ranking stay dark until validated. Generates the claims calibration needs without ranking anyone on untested arithmetic. |
| **Ship fully, tune with real data** | Fastest to a working product. The first cohort is ranked on guesses, and re-tuning later moves everyone's score — with a snapshot reason explaining it, but still. |
| **Tune before anything is shown** | Nobody ever sees an untested score. Requires building the seed set by hand first, delaying launch on work that a week of real claims would do better. |

The first fits the architecture best — scoring already runs independently of search, and
`user_skills.standing` already gates what is rankable — but it is a product decision, not an
architectural one.

## Ongoing

Calibration is not a one-off. Every rubric version bump re-opens all four questions, and the
sweep it triggers (ADR-0004) is the moment to re-measure rather than assume.

Keep the seed set. It is the only fixed point across rubric versions, and without it "did v2
improve on v1" is unanswerable.
