/* ==========================================================================
   GitCherryPick — seeded discovery dataset.

   Numbers on screen are not hand-written. The normalisation, the PR score,
   the skill score, the overall score and the generalist score are computed
   here with the arithmetic from ADR-0005, from per-PR judged dimensions and
   real repository facts — so a scorecard breakdown always adds up to the
   number printed beside it.

   Repositories are real open-source projects; their star/fork/dependent
   counts are approximate and are sample data, not verified platform facts.
   Contributors are fictional.
   ========================================================================== */
(function (global) {
  'use strict';

  var TODAY = new Date('2026-09-12T00:00:00Z');
  var RUBRIC = 'rubric-v1.3';

  /* ---- platform-wide maxima + sampled quantiles (global_norms) ---------- */
  var NORMS = {
    repo_stars:        { max: 190000, q: { p10: 5, p25: 30, p50: 240, p75: 2100, p90: 12000, p99: 90000 } },
    repo_forks:        { max: 52000,  q: { p10: 1, p25: 8, p50: 60, p75: 500, p90: 3000, p99: 22000 } },
    repo_contributors: { max: 3400,   q: { p10: 1, p25: 3, p50: 12, p75: 60, p90: 240, p99: 1400 } },
    dependents:        { max: 480000, q: { p10: 0, p25: 2, p50: 40, p75: 900, p90: 11000, p99: 160000 } },
    package_downloads: { max: 92000000, q: { p10: 0, p25: 120, p50: 4000, p75: 90000, p90: 1200000, p99: 28000000 } },
    review_comments:   { max: 340,    q: { p10: 0, p25: 1, p50: 4, p75: 12, p90: 31, p99: 140 } },
    reviews:           { max: 46,     q: { p10: 0, p25: 1, p50: 2, p75: 4, p90: 9, p99: 24 } },
    participants:      { max: 28,     q: { p10: 1, p25: 1, p50: 2, p75: 4, p90: 8, p99: 18 } }
  };
  var QPOINTS = [[0, 0], [0.10, 'p10'], [0.25, 'p25'], [0.50, 'p50'], [0.75, 'p75'], [0.90, 'p90'], [0.99, 'p99'], [1, 'max']];

  function pctNorm(metric, v) {
    var n = NORMS[metric], prev = 0, prevV = 0, i, pt, bound;
    for (i = 1; i < QPOINTS.length; i++) {
      pt = QPOINTS[i];
      bound = pt[1] === 'max' ? n.max : n.q[pt[1]];
      if (v <= bound) {
        if (bound === prevV) return pt[0];
        return prev + (pt[0] - prev) * ((v - prevV) / (bound - prevV));
      }
      prev = pt[0]; prevV = bound;
    }
    return 1;
  }
  /* norm(v) = beta*log_norm + (1-beta)*pct_norm, beta = 0.5 */
  function norm(metric, v) {
    if (v == null) return null;
    var n = NORMS[metric];
    var logN = Math.log(1 + v) / Math.log(1 + n.max);
    return 0.5 * Math.min(1, logN) + 0.5 * pctNorm(metric, v);
  }

  /* ---- skill catalogue -------------------------------------------------- */
  var SKILLS = [
    { slug: 'go', name: 'Go', category: 'language', mode: 'standard' },
    { slug: 'kubernetes', name: 'Kubernetes', category: 'infrastructure', mode: 'standard' },
    { slug: 'postgres', name: 'PostgreSQL', category: 'database', mode: 'standard' },
    { slug: 'rust', name: 'Rust', category: 'language', mode: 'standard' },
    { slug: 'typescript', name: 'TypeScript', category: 'language', mode: 'standard' },
    { slug: 'react', name: 'React', category: 'frontend', mode: 'standard' },
    { slug: 'observability', name: 'Observability', category: 'practice', mode: 'standard' },
    { slug: 'distributed-systems', name: 'Distributed systems', category: 'practice', mode: 'standard' },
    { slug: 'terraform', name: 'Terraform', category: 'infrastructure', mode: 'standard' },
    { slug: 'pr-review', name: 'PR Review', category: 'practice', mode: 'judged_only' }
  ];
  var SKILL_BY_SLUG = {};
  SKILLS.forEach(function (s) { SKILL_BY_SLUG[s.slug] = s; });

  /* ---- repositories (real projects, approximate sample metrics) --------- */
  function repo(o) { return o; }
  var REPOS = [
    repo({ slug: 'kubernetes/kubernetes', org: 'kubernetes', mark: 'kubernetes.png', markSize: 120, stars: 116000, forks: 40800, contributors: 3400, dependents: 12400, downloads: null, skills: ['go', 'kubernetes', 'distributed-systems'] }),
    repo({ slug: 'etcd-io/etcd', org: 'etcd-io', mark: 'etcd-io.png', markSize: 120, stars: 48500, forks: 9900, contributors: 1000, dependents: 26000, downloads: null, skills: ['go', 'distributed-systems'] }),
    repo({ slug: 'prometheus/prometheus', org: 'prometheus', mark: 'prometheus.png', markSize: 114, stars: 57000, forks: 9300, contributors: 950, dependents: 4200, downloads: null, skills: ['go', 'observability'] }),
    repo({ slug: 'grafana/grafana', org: 'grafana', mark: 'grafana.png', markSize: 120, stars: 66000, forks: 12400, contributors: 2100, dependents: 900, downloads: null, skills: ['typescript', 'react', 'observability', 'go'] }),
    repo({ slug: 'hashicorp/terraform', org: 'hashicorp', mark: 'hashicorp.png', markSize: 120, stars: 44000, forks: 9800, contributors: 1800, dependents: 2600, downloads: null, skills: ['go', 'terraform'] }),
    repo({ slug: 'pgvector/pgvector', org: 'pgvector', mark: 'pgvector.png', markSize: 120, stars: 14500, forks: 700, contributors: 62, dependents: 1800, downloads: null, skills: ['postgres'] }),
    repo({ slug: 'go-chi/chi', org: 'go-chi', mark: 'go-chi.png', markSize: 120, stars: 19500, forks: 1000, contributors: 130, dependents: 41000, downloads: null, skills: ['go'] }),
    repo({ slug: 'jackc/pgx', org: 'jackc', mark: 'jackc.png', markSize: 420, stars: 11500, forks: 900, contributors: 230, dependents: 38000, downloads: null, skills: ['go', 'postgres'] }),
    repo({ slug: 'spf13/cobra', org: 'spf13', mark: 'spf13.png', markSize: 120, stars: 39500, forks: 2900, contributors: 400, dependents: 210000, downloads: null, skills: ['go'] }),
    repo({ slug: 'open-telemetry/opentelemetry-go', org: 'open-telemetry', mark: 'open-telemetry.png', markSize: 120, stars: 5600, forks: 1150, contributors: 400, dependents: 33000, downloads: null, skills: ['go', 'observability'] }),
    repo({ slug: 'cilium/cilium', org: 'cilium', mark: 'cilium.png', markSize: 120, stars: 21000, forks: 3100, contributors: 800, dependents: 700, downloads: null, skills: ['go', 'kubernetes', 'distributed-systems'] }),
    repo({ slug: 'timescale/timescaledb', org: 'timescale', mark: 'timescale.png', markSize: 120, stars: 18500, forks: 950, contributors: 120, dependents: 300, downloads: null, skills: ['postgres'] }),
    repo({ slug: 'vitessio/vitess', org: 'vitessio', mark: 'vitessio.png', markSize: 120, stars: 19000, forks: 2200, contributors: 500, dependents: 400, downloads: null, skills: ['go', 'distributed-systems', 'postgres'] }),
    repo({ slug: 'helm/helm', org: 'helm', mark: 'helm.png', markSize: 120, stars: 27500, forks: 7300, contributors: 900, dependents: 6800, downloads: null, skills: ['go', 'kubernetes'] }),
    repo({ slug: 'containerd/containerd', org: 'containerd', mark: 'containerd.png', markSize: 46, stars: 18500, forks: 3600, contributors: 600, dependents: 22000, downloads: null, skills: ['go', 'kubernetes'] }),
    repo({ slug: 'supabase/supabase', org: 'supabase', mark: 'supabase.png', markSize: 120, stars: 78000, forks: 8100, contributors: 900, dependents: 1200, downloads: 3500000, skills: ['typescript', 'react', 'postgres'] }),
    repo({ slug: 'tokio-rs/tokio', org: 'tokio-rs', mark: 'tokio-rs.png', markSize: 108, stars: 28000, forks: 2600, contributors: 520, dependents: 190000, downloads: null, skills: ['rust', 'distributed-systems'] }),
    repo({ slug: 'denoland/deno', org: 'denoland', mark: 'denoland.png', markSize: 120, stars: 98000, forks: 5400, contributors: 950, dependents: 3100, downloads: null, skills: ['rust', 'typescript'] }),
    repo({ slug: 'vercel/next.js', org: 'vercel', mark: 'vercel.png', markSize: 120, stars: 130000, forks: 28000, contributors: 3300, dependents: 480000, downloads: 9200000, skills: ['typescript', 'react'] })
  ];
  var REPO_BY_SLUG = {};
  REPOS.forEach(function (r) {
    REPO_BY_SLUG[r.slug] = r;
    /* R = .35 stars + .15 forks + .20 contributors + .20 dependents + .10 downloads,
       missing metrics dropped and the remaining weights renormalised. */
    var parts = [
      [0.35, norm('repo_stars', r.stars)],
      [0.15, norm('repo_forks', r.forks)],
      [0.20, norm('repo_contributors', r.contributors)],
      [0.20, norm('dependents', r.dependents)],
      [0.10, norm('package_downloads', r.downloads)]
    ].filter(function (p) { return p[1] != null; });
    var wsum = parts.reduce(function (a, p) { return a + p[0]; }, 0);
    r.reach = parts.reduce(function (a, p) { return a + p[0] * p[1]; }, 0) / wsum;
    r.name = r.slug.split('/')[1];
  });

  /* ---- deterministic pseudo-randomness ---------------------------------- */
  function hash(str) {
    var h = 2166136261, i;
    for (i = 0; i < str.length; i++) { h ^= str.charCodeAt(i); h = Math.imul(h, 16777619); }
    return h >>> 0;
  }
  function rng(seed) {
    var a = hash(seed);
    return function () {
      a |= 0; a = (a + 0x6D2B79F5) | 0;
      var t = Math.imul(a ^ (a >>> 15), 1 | a);
      t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
      return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
    };
  }
  function pick(arr, r) { return arr[Math.floor(r() * arr.length) % arr.length]; }
  function clamp(v, lo, hi) { return Math.max(lo, Math.min(hi, v)); }
  function round1(v) { return Math.round(v * 10) / 10; }

  /* ---- pull-request titles, per skill ----------------------------------- */
  var TITLES = {
    go: [
      'Avoid a goroutine leak when the watch context is cancelled mid-stream',
      'Replace the reflection-based decoder with generated accessors',
      'Make the retry budget deterministic under clock skew',
      'Fix a data race between the informer resync and the delete handler',
      'Reduce allocations in the hot path of the request router',
      'Propagate context deadlines through the storage interface'
    ],
    kubernetes: [
      'Requeue with backoff instead of dropping the object on conflict',
      'Make the operator idempotent when a finalizer is removed twice',
      'Fix status subresource drift after a partial apply',
      'Stop the controller from hot-looping on an unschedulable pod',
      'Honour terminationGracePeriodSeconds in the eviction path',
      'Validate CRD defaults at admission rather than at reconcile'
    ],
    postgres: [
      'Use a partial index to keep the queue scan off the heap',
      'Fix planner misestimation on correlated jsonb predicates',
      'Take the advisory lock before, not inside, the retry loop',
      'Avoid a full table rewrite when widening the column type',
      'Batch the copy path to cut WAL volume on bulk import',
      'Correct the visibility check in the concurrent reindex path'
    ],
    rust: [
      'Remove the unsound Send impl on the shared cursor',
      'Replace the spin loop with a parked waker on backpressure',
      'Make the timer wheel cancellation-safe under select!',
      'Cut a lifetime from the borrow chain in the frame decoder',
      'Fix a use-after-free in the buffer pool reclaim path',
      'Make the panic path unwind the connection state cleanly'
    ],
    typescript: [
      'Narrow the discriminated union so the compiler catches missing cases',
      'Replace the any-typed transport layer with generated request types',
      'Fix the inference regression on generic default parameters',
      'Stop the build from shipping type-only imports at runtime',
      'Make the config loader fail at compile time on unknown keys',
      'Type the plugin registry so extensions cannot widen the contract'
    ],
    react: [
      'Stop re-rendering the whole table on a single row selection',
      'Move the derived filter state out of an effect and into render',
      'Fix focus loss when the dropdown re-mounts on data refresh',
      'Make the virtualised list keyboard-navigable',
      'Cancel in-flight requests when the panel unmounts',
      'Restore scroll position when returning from the detail view'
    ],
    observability: [
      'Stop cardinality explosion from unbounded label values',
      'Correct the histogram bucket boundaries on re-scrape',
      'Propagate the trace context across the async boundary',
      'Make the exemplar sampler respect the parent decision',
      'Fix the staleness marker on a target that disappears mid-scrape',
      'Reduce the memory footprint of the in-memory series index'
    ],
    'distributed-systems': [
      'Fix the lease renewal race that could elect two leaders',
      'Make the snapshot transfer resumable after a follower restart',
      'Bound the write buffer so a slow follower cannot OOM the leader',
      'Correct the read-index path under a stale term',
      'Make membership changes single-step to avoid a split quorum',
      'Fence the old primary before promoting the replica'
    ],
    terraform: [
      'Stop the provider from forcing replacement on a computed field',
      'Make the state upgrade path idempotent across two schema versions',
      'Fix the plan diff for nested sets with unknown values',
      'Surface the underlying API error instead of a generic failure',
      'Support import for resources created outside the module',
      'Avoid a spurious drift on tags applied by the platform'
    ],
    'pr-review': [
      'Review: caught an unbounded retry that would amplify an outage',
      'Review: argued the migration needed a backfill before the constraint',
      'Review: rejected the cache layer and proposed the index instead',
      'Review: found the off-by-one in the pagination cursor',
      'Review: pushed back on the API shape before it became public',
      'Review: identified the missing fsync on the durability path'
    ]
  };

  var RATIONALE = {
    high: [
      'Substantial change in a codebase with a high review bar. The author located the root cause rather than the symptom, and the review thread shows them defending the approach against two experienced maintainers without conceding correctness.',
      'The diff is small and the reasoning behind it is not. Reproduction, a regression test that fails without the fix, and a clear explanation of why the obvious fix would have been wrong.',
      'Work of real consequence: the change touches a path every consumer of this project executes, and the author carried it through a long review with the design intact.'
    ],
    mid: [
      'Solid, well-scoped contribution. The change is correct and tested, the review was routine, and the author responded to feedback promptly. Competent rather than exceptional.',
      'A real fix to a real problem, with a test that pins the behaviour. The conversation stayed short because there was little to argue with.',
      'Clean implementation against an established pattern in the codebase. Limited novelty, but the execution is careful and the tests are meaningful.'
    ],
    low: [
      'Narrow change with a modest blast radius. Correct, reviewed quickly, and it demonstrates familiarity with the codebase more than depth in the skill claimed.',
      'Straightforward fix following an existing pattern. The evidence supports the skill but does not stretch it.',
      'Small, correct and uncontroversial. It counts, and it is the weakest of the five pieces of evidence supplied.'
    ],
    review: [
      'A single precise comment changed the shape of the change. The reviewer identified a failure mode the author had not considered and stayed with the thread until it was resolved.',
      'Rigorous review that separated the blocking concern from the stylistic one and said which was which. The author left the thread with a better design and no bruises.',
      'The reviewer asked for the one test that would have caught the regression, and explained why the proposed alternative was worse. Short, decisive and correct.'
    ]
  };

  /* ---- contributor seeds ------------------------------------------------ */
  /* calibre drives the judged dimensions; prs drives standing (>=5 primary). */
  function C(o) { return o; }
  var SEEDS = [
    C({ login: 'alice-ng', name: 'Alice Ng', loc: 'Wellington, NZ', tz: 'UTC+12', joined: '2026-02-11', followers: 1240, merged: 412, repos: 34,
        headline: 'Runtime and storage internals. Ten years of Go, mostly where it meets a database.',
        avail: { status: 'looking_for_job', daysAgo: 2 },
        skills: [ { slug: 'go', prs: 6, cal: 0.78 }, { slug: 'postgres', prs: 5, cal: 0.72 }, { slug: 'distributed-systems', prs: 3, cal: 0.69 } ],
        projects: [ { repo: 'jackc/pgx', maintainer: false, skills: ['go', 'postgres'] } ] }),
    C({ login: 'bferreira', name: 'Bob Ferreira', loc: 'Porto, PT', tz: 'UTC+1', joined: '2026-02-14', followers: 610, merged: 288, repos: 51,
        headline: 'Platform generalist. Comfortable anywhere between the control plane and the query planner.',
        avail: { status: 'looking_for_freelance', daysAgo: 5 },
        skills: [ { slug: 'go', prs: 5, cal: 0.56 }, { slug: 'kubernetes', prs: 5, cal: 0.52 }, { slug: 'postgres', prs: 5, cal: 0.47 }, { slug: 'terraform', prs: 2, cal: 0.5 } ],
        projects: [ { repo: 'helm/helm', maintainer: false, skills: ['kubernetes'] } ] }),
    C({ login: 'cdiaz', name: 'Carol Diaz', loc: 'Bogotá, CO', tz: 'UTC-5', joined: '2026-01-30', followers: 2110, merged: 196, repos: 12,
        headline: 'Postgres performance. Query plans, lock contention, and the migrations nobody wants to run.',
        avail: { status: 'looking_for_job', daysAgo: 46 },
        skills: [ { slug: 'postgres', prs: 6, cal: 0.82 }, { slug: 'go', prs: 2, cal: 0.6 } ],
        projects: [ { repo: 'timescale/timescaledb', maintainer: true, skills: ['postgres'] } ] }),
    C({ login: 'dokafor', name: 'Dave Okafor', loc: 'Lagos, NG', tz: 'UTC+1', joined: '2026-03-02', followers: 480, merged: 154, repos: 19,
        headline: 'Cluster operators and the unglamorous parts of reconciliation.',
        avail: { status: 'looking_for_job', daysAgo: 9 },
        skills: [ { slug: 'kubernetes', prs: 5, cal: 0.6 }, { slug: 'go', prs: 4, cal: 0.85 } ],
        projects: [] }),
    C({ login: 'imara-k', name: 'Imara Kaunda', loc: 'Nairobi, KE', tz: 'UTC+3', joined: '2026-01-18', followers: 3320, merged: 604, repos: 41,
        headline: 'Consensus, leases and the failure modes that only show up at 3am.',
        avail: { status: 'looking_for_job', daysAgo: 1 },
        skills: [ { slug: 'distributed-systems', prs: 7, cal: 0.9 }, { slug: 'go', prs: 6, cal: 0.83 }, { slug: 'pr-review', prs: 5, cal: 0.76 } ],
        projects: [ { repo: 'etcd-io/etcd', maintainer: true, skills: ['distributed-systems', 'go'] } ] }),
    C({ login: 'jonas-w', name: 'Jonas Weber', loc: 'Leipzig, DE', tz: 'UTC+2', joined: '2026-02-02', followers: 890, merged: 233, repos: 27,
        headline: 'Async Rust, mostly in the parts that are hard to make cancellation-safe.',
        avail: { status: 'open_to_freelance', daysAgo: 6 },
        skills: [ { slug: 'rust', prs: 6, cal: 0.86 }, { slug: 'distributed-systems', prs: 4, cal: 0.74 } ],
        projects: [ { repo: 'tokio-rs/tokio', maintainer: false, skills: ['rust'] } ] }),
    C({ login: 'priyasr', name: 'Priya Sridharan', loc: 'Bengaluru, IN', tz: 'UTC+5:30', joined: '2026-01-22', followers: 1740, merged: 377, repos: 22,
        headline: 'Metrics pipelines that stay up when the thing they are measuring does not.',
        avail: { status: 'looking_for_job', daysAgo: 3 },
        skills: [ { slug: 'observability', prs: 6, cal: 0.84 }, { slug: 'go', prs: 5, cal: 0.7 }, { slug: 'kubernetes', prs: 3, cal: 0.63 } ],
        projects: [ { repo: 'prometheus/prometheus', maintainer: false, skills: ['observability', 'go'] } ] }),
    C({ login: 'tomasz-l', name: 'Tomasz Lewandowski', loc: 'Kraków, PL', tz: 'UTC+2', joined: '2026-02-20', followers: 520, merged: 141, repos: 16,
        headline: 'Provider engineering. If the plan diff lies, I find out why.',
        avail: { status: 'looking_for_freelance', daysAgo: 11 },
        skills: [ { slug: 'terraform', prs: 5, cal: 0.71 }, { slug: 'go', prs: 5, cal: 0.58 } ],
        projects: [ { repo: 'hashicorp/terraform', maintainer: false, skills: ['terraform'] } ] }),
    C({ login: 'yuki-t', name: 'Yuki Tanaka', loc: 'Osaka, JP', tz: 'UTC+9', joined: '2026-01-14', followers: 2680, merged: 512, repos: 38,
        headline: 'Frontend at scale. Rendering budgets, focus management, and tables with a hundred thousand rows.',
        avail: { status: 'looking_for_job', daysAgo: 4 },
        skills: [ { slug: 'typescript', prs: 6, cal: 0.87 }, { slug: 'react', prs: 5, cal: 0.81 } ],
        projects: [ { repo: 'grafana/grafana', maintainer: false, skills: ['typescript', 'react'] } ] }),
    C({ login: 'ana-mrz', name: 'Ana Moraes', loc: 'São Paulo, BR', tz: 'UTC-3', joined: '2026-03-09', followers: 340, merged: 97, repos: 11,
        headline: 'Container runtimes and the boundary between the kernel and the scheduler.',
        avail: { status: 'looking_for_job', daysAgo: 21 },
        skills: [ { slug: 'kubernetes', prs: 5, cal: 0.66 }, { slug: 'go', prs: 5, cal: 0.61 } ],
        projects: [] }),
    C({ login: 'hkoskinen', name: 'Henna Koskinen', loc: 'Helsinki, FI', tz: 'UTC+3', joined: '2026-02-05', followers: 1420, merged: 264, repos: 24,
        headline: 'Reviewer first, author second. I have opinions about migrations and I write them down.',
        avail: { status: 'looking_for_job', daysAgo: 7 },
        skills: [ { slug: 'pr-review', prs: 5, cal: 0.79 }, { slug: 'postgres', prs: 5, cal: 0.68 }, { slug: 'go', prs: 3, cal: 0.64 } ],
        projects: [ { repo: 'pgvector/pgvector', maintainer: false, skills: ['postgres'] } ] }),
    C({ login: 'marco-dv', name: 'Marco De Vita', loc: 'Bologna, IT', tz: 'UTC+2', joined: '2026-03-15', followers: 260, merged: 88, repos: 9,
        headline: 'Networking dataplane work, eBPF-adjacent.',
        avail: { status: 'open_to_freelance', daysAgo: 13 },
        skills: [ { slug: 'go', prs: 5, cal: 0.64 }, { slug: 'kubernetes', prs: 4, cal: 0.7 }, { slug: 'distributed-systems', prs: 2, cal: 0.55 } ],
        projects: [ { repo: 'cilium/cilium', maintainer: false, skills: ['kubernetes', 'go'] } ] }),
    C({ login: 'nadia-hr', name: 'Nadia Haddad', loc: 'Tunis, TN', tz: 'UTC+1', joined: '2026-02-26', followers: 760, merged: 173, repos: 15,
        headline: 'Sharding, routing and the migrations that move data while it is being read.',
        avail: { status: 'looking_for_job', daysAgo: 8 },
        skills: [ { slug: 'distributed-systems', prs: 5, cal: 0.75 }, { slug: 'postgres', prs: 5, cal: 0.73 }, { slug: 'go', prs: 4, cal: 0.66 } ],
        projects: [ { repo: 'vitessio/vitess', maintainer: false, skills: ['distributed-systems', 'postgres'] } ] }),
    C({ login: 'sven-b', name: 'Sven Bergström', loc: 'Gothenburg, SE', tz: 'UTC+2', joined: '2026-01-27', followers: 1980, merged: 341, repos: 29,
        headline: 'Runtime performance. Allocation profiles, not micro-benchmarks.',
        avail: { status: 'looking_for_job', daysAgo: 62 },
        skills: [ { slug: 'go', prs: 6, cal: 0.88 }, { slug: 'observability', prs: 4, cal: 0.71 } ],
        projects: [ { repo: 'go-chi/chi', maintainer: false, skills: ['go'] } ] }),
    C({ login: 'rmehta', name: 'Rohan Mehta', loc: 'Pune, IN', tz: 'UTC+5:30', joined: '2026-03-20', followers: 410, merged: 119, repos: 13,
        headline: 'Type systems in anger. I make the compiler carry the invariants.',
        avail: { status: 'looking_for_freelance', daysAgo: 10 },
        skills: [ { slug: 'typescript', prs: 5, cal: 0.74 }, { slug: 'react', prs: 4, cal: 0.68 } ],
        projects: [] }),
    C({ login: 'lgrant', name: 'Lena Grant', loc: 'Manchester, UK', tz: 'UTC+1', joined: '2026-02-08', followers: 1130, merged: 207, repos: 18,
        headline: 'Tracing and the async boundaries where context goes missing.',
        avail: { status: 'looking_for_job', daysAgo: 12 },
        skills: [ { slug: 'observability', prs: 5, cal: 0.77 }, { slug: 'go', prs: 5, cal: 0.65 }, { slug: 'pr-review', prs: 3, cal: 0.8 } ],
        projects: [ { repo: 'open-telemetry/opentelemetry-go', maintainer: false, skills: ['observability', 'go'] } ] }),
    C({ login: 'omar-fz', name: 'Omar Faiz', loc: 'Amman, JO', tz: 'UTC+3', joined: '2026-03-25', followers: 190, merged: 64, repos: 7,
        headline: 'Build tooling and the CLI surfaces people actually type into.',
        avail: { status: 'looking_for_job', daysAgo: 18 },
        skills: [ { slug: 'go', prs: 5, cal: 0.53 }, { slug: 'terraform', prs: 3, cal: 0.57 } ],
        projects: [] }),
    C({ login: 'kchen', name: 'Kim Chen', loc: 'Vancouver, CA', tz: 'UTC-7', joined: '2026-01-11', followers: 3010, merged: 588, repos: 44,
        headline: 'Query planning and storage engines. Most of my best work is a one-line diff.',
        avail: { status: 'looking_for_job', daysAgo: 2 },
        skills: [ { slug: 'postgres', prs: 7, cal: 0.89 }, { slug: 'rust', prs: 5, cal: 0.76 }, { slug: 'pr-review', prs: 4, cal: 0.82 } ],
        projects: [ { repo: 'timescale/timescaledb', maintainer: false, skills: ['postgres'] }, { repo: 'tokio-rs/tokio', maintainer: false, skills: ['rust'] } ] }),
    C({ login: 'fatou-d', name: 'Fatou Diop', loc: 'Dakar, SN', tz: 'UTC+0', joined: '2026-02-17', followers: 670, merged: 158, repos: 14,
        headline: 'Design systems and the accessibility work that gets cut first.',
        avail: { status: 'looking_for_job', daysAgo: 5 },
        skills: [ { slug: 'react', prs: 5, cal: 0.79 }, { slug: 'typescript', prs: 5, cal: 0.72 } ],
        projects: [ { repo: 'supabase/supabase', maintainer: false, skills: ['react', 'typescript'] } ] }),
    C({ login: 'dmitri-v', name: 'Dmitri Volkov', loc: 'Tbilisi, GE', tz: 'UTC+4', joined: '2026-03-06', followers: 830, merged: 189, repos: 21,
        headline: 'Unsafe Rust reviewed carefully, or not written at all.',
        avail: { status: 'open_to_freelance', daysAgo: 16 },
        skills: [ { slug: 'rust', prs: 5, cal: 0.8 }, { slug: 'typescript', prs: 2, cal: 0.6 } ],
        projects: [] }),
    C({ login: 'sofia-mr', name: 'Sofia Mendes Ribeiro', loc: 'Lisbon, PT', tz: 'UTC+1', joined: '2026-01-25', followers: 1560, merged: 301, repos: 26,
        headline: 'Operators, admission control and the CRD lifecycle.',
        avail: { status: 'looking_for_job', daysAgo: 3 },
        skills: [ { slug: 'kubernetes', prs: 6, cal: 0.83 }, { slug: 'go', prs: 5, cal: 0.75 }, { slug: 'terraform', prs: 3, cal: 0.61 } ],
        projects: [ { repo: 'kubernetes/kubernetes', maintainer: false, skills: ['kubernetes', 'go'] } ] }),
    C({ login: 'aaron-k', name: 'Aaron Kessler', loc: 'Austin, US', tz: 'UTC-5', joined: '2026-04-02', followers: 220, merged: 71, repos: 8,
        headline: 'Recently switched from data engineering to platform work.',
        avail: { status: 'looking_for_job', daysAgo: 6 },
        skills: [ { slug: 'go', prs: 3, cal: 0.62 }, { slug: 'postgres', prs: 3, cal: 0.58 } ],
        projects: [] }),
    C({ login: 'wei-lin', name: 'Wei Lin', loc: 'Taipei, TW', tz: 'UTC+8', joined: '2026-02-12', followers: 1290, merged: 246, repos: 20,
        headline: 'Scheduling, eviction and the parts of the kubelet nobody enjoys.',
        avail: { status: 'looking_for_job', daysAgo: 31 },
        skills: [ { slug: 'kubernetes', prs: 5, cal: 0.78 }, { slug: 'go', prs: 5, cal: 0.69 } ],
        projects: [ { repo: 'containerd/containerd', maintainer: false, skills: ['kubernetes'] } ] }),
    C({ login: 'greta-ohl', name: 'Greta Öhlund', loc: 'Malmö, SE', tz: 'UTC+2', joined: '2026-03-11', followers: 950, merged: 202, repos: 17,
        headline: 'Runtime and bundler internals. I like problems with a reproduction.',
        avail: { status: 'not_looking', daysAgo: 1 },
        skills: [ { slug: 'rust', prs: 5, cal: 0.85 }, { slug: 'typescript', prs: 5, cal: 0.8 } ],
        projects: [ { repo: 'denoland/deno', maintainer: false, skills: ['rust', 'typescript'] } ] })
  ];

  /* ---- build the evaluated pool ----------------------------------------- */
  function reposForSkill(slug) {
    var list = REPOS.filter(function (r) { return r.skills.indexOf(slug) >= 0; });
    return list.length ? list : REPOS;
  }
  function daysBetween(a, b) { return Math.round((b - a) / 86400000); }
  function isoDaysAgo(d) { return new Date(TODAY.getTime() - d * 86400000).toISOString().slice(0, 10); }

  function buildEvidence(seed, skillRef) {
    var r = rng(seed.login + ':' + skillRef.slug);
    var pool = reposForSkill(skillRef.slug);
    var titles = TITLES[skillRef.slug];
    var judgedOnly = SKILL_BY_SLUG[skillRef.slug].mode === 'judged_only';
    var items = [], i;
    for (i = 0; i < skillRef.prs; i++) {
      var rp = pool[Math.floor(r() * pool.length) % pool.length];
      var base = 38 + skillRef.cal * 54 + (r() * 14 - 7);
      function dim(spread) { return Math.round(clamp(base + (r() * spread - spread / 2), 12, 99)); }
      var d = {
        substance: dim(18), complexity: dim(20), conversation_quality: dim(22),
        craft: dim(16), skill_specificity: dim(18)
      };
      var Q = 0.30 * d.substance + 0.25 * d.complexity + 0.20 * d.conversation_quality
            + 0.15 * d.craft + 0.10 * d.skill_specificity;

      var scale = Math.min(1, Math.log(1 + rp.contributors) / Math.log(1 + 3400));
      var facts = {
        reviews: Math.max(1, Math.round(1 + r() * 9 * (0.4 + scale))),
        review_comments: Math.max(0, Math.round(r() * 44 * (0.3 + scale) * (0.5 + skillRef.cal))),
        participants: Math.max(1, Math.round(1 + r() * 7 * (0.4 + scale))),
        additions: Math.round(8 + r() * 620),
        deletions: Math.round(2 + r() * 240),
        files: Math.round(1 + r() * 18)
      };
      var E = 0.40 * norm('review_comments', facts.review_comments)
            + 0.30 * norm('reviews', facts.reviews)
            + 0.30 * norm('participants', facts.participants);

      var R = rp.reach;
      var proj = (seed.projects || []).filter(function (p) { return p.repo === rp.slug && p.maintainer; })[0];
      if (proj) R = Math.min(1, R * 1.25);

      var score = judgedOnly
        ? 100 * (0.80 * (Q / 100) + 0.20 * R)
        : 100 * (0.70 * (Q / 100) + 0.20 * R + 0.10 * E);

      var band = Q >= 78 ? 'high' : (Q >= 58 ? 'mid' : 'low');
      items.push({
        skill: skillRef.slug,
        repo: rp.slug,
        org: rp.org,
        mark: rp.mark,
        markSize: rp.markSize,
        number: 1000 + Math.floor(r() * 58000),
        title: titles[i % titles.length],
        role: judgedOnly ? 'reviewer' : 'author',
        mergedAt: isoDaysAgo(Math.round(20 + r() * 700)),
        dims: d, q: round1(Q), reach: R, engagement: E,
        maintainer: !!proj,
        judgedOnly: judgedOnly,
        facts: facts,
        repoFacts: { stars: rp.stars, forks: rp.forks, contributors: rp.contributors, dependents: rp.dependents, downloads: rp.downloads },
        rationale: pick(judgedOnly ? RATIONALE.review : RATIONALE[band], r),
        score: round1(score)
      });
    }
    return items;
  }

  function buildContributor(seed) {
    var c = {
      id: seed.login,
      login: seed.login,
      name: seed.name,
      initials: seed.name.split(/\s+/).map(function (w) { return w[0]; }).slice(0, 2).join(''),
      tint: hash(seed.login) % 8,
      location: seed.loc,
      timezone: seed.tz,
      headline: seed.headline,
      joined: seed.joined,
      followers: seed.followers,
      mergedContributions: seed.merged,
      reposTouched: seed.repos,
      rubricVersion: RUBRIC,
      evidence: [],
      skills: [],
      rankOverall: null,
      rankGeneralist: null,
      projects: (seed.projects || []).map(function (p) {
        var rp = REPO_BY_SLUG[p.repo];
        return {
          repo: rp.slug, org: rp.org, mark: rp.mark, markSize: rp.markSize,
          maintainer: p.maintainer, skills: p.skills,
          reach: p.maintainer ? Math.min(1, rp.reach * 1.25) : rp.reach,
          facts: { stars: rp.stars, forks: rp.forks, contributors: rp.contributors, dependents: rp.dependents, downloads: rp.downloads }
        };
      })
    };

    var status = seed.avail.status;
    var confirmedDaysAgo = seed.avail.daysAgo;
    c.availability = {
      status: status,
      lastConfirmedAt: isoDaysAgo(confirmedDaysAgo),
      active: confirmedDaysAgo <= 15,
      inactiveForDays: Math.max(0, confirmedDaysAgo - 15)
    };

    seed.skills.forEach(function (sk) {
      var ev = buildEvidence(seed, sk);
      c.evidence = c.evidence.concat(ev);

      var scores = ev.map(function (e) { return e.score; }).sort(function (a, b) { return b - a; });
      var n = scores.length;
      var prComponent = n >= 5
        ? scores.slice(0, 5).reduce(function (a, b) { return a + b; }, 0) / 5
        : (scores.reduce(function (a, b) { return a + b; }, 0) / n) * (n / 5);

      var judgedOnly = SKILL_BY_SLUG[sk.slug].mode === 'judged_only';
      var relevantProjects = c.projects.filter(function (p) { return p.skills.indexOf(sk.slug) >= 0; });
      var projComponent = (judgedOnly || !relevantProjects.length) ? 0
        : relevantProjects.reduce(function (a, p) { return a + p.reach; }, 0) / relevantProjects.length;

      var skillScore = judgedOnly ? prComponent : 100 * (0.85 * (prComponent / 100) + 0.15 * projComponent);

      c.skills.push({
        slug: sk.slug,
        name: SKILL_BY_SLUG[sk.slug].name,
        mode: SKILL_BY_SLUG[sk.slug].mode,
        prCount: n,
        standing: n >= 5 ? 'primary' : 'secondary',
        score: round1(skillScore),
        prComponent: round1(prComponent),
        projectComponent: Math.round(projComponent * 100) / 100,
        rank: null
      });
    });

    c.skills.sort(function (a, b) { return b.score - a.score; });
    c.evidence.sort(function (a, b) { return b.score - a.score; });
    c.newestEvidence = c.evidence.reduce(function (a, e) { return e.mergedAt > a ? e.mergedAt : a; }, '0000-00-00');
    c.evidenceAgeMonths = Math.round(daysBetween(new Date(c.newestEvidence + 'T00:00:00Z'), TODAY) / 30.44);

    /* overall + generalist over the adjusted, descending value list */
    var values = c.skills.map(function (s) {
      var v = s.score;
      if (s.slug === 'pr-review') v = Math.min(100, v * 1.2);
      if (s.standing === 'secondary') v = v * 0.5;
      return { slug: s.slug, v: v };
    }).sort(function (a, b) { return b.v - a.v; });

    c.valueLadder = values.map(function (x) { return { slug: x.slug, value: round1(x.v) }; });

    var hasPrimary = c.skills.some(function (s) { return s.standing === 'primary'; });
    if (!hasPrimary) {
      c.overall = null; c.generalist = null; c.breadth = null;
      c.unrankedReason = 'no_primary_skill';
    } else {
      var v1 = values[0].v, num = 0, den = 0, d = 0.5, i;
      for (i = 1; i < values.length; i++) {
        num += (values[i].v / 100) * Math.pow(d, i);
        den += Math.pow(d, i);
      }
      var B = den ? num / den : 0;
      c.breadth = Math.round(B * 100) / 100;
      c.overall = round1(v1 + (100 - v1) * 0.5 * B);
      c.generalist = round1(values.reduce(function (a, x, idx) { return a + x.v / Math.sqrt(idx + 1); }, 0));
    }
    return c;
  }

  var CONTRIBUTORS = SEEDS.map(buildContributor);
  var BY_ID = {};
  CONTRIBUTORS.forEach(function (c) { BY_ID[c.id] = c; });

  /* ---- global ranking ---------------------------------------------------
     Rank is computed over the whole scored population minus not_looking, and
     survives lapsing (ADR-0008 §1a). Filtering a list never recomputes it. */
  function assignRanks() {
    var ranked = CONTRIBUTORS.filter(function (c) {
      return c.overall != null && c.availability.status !== 'not_looking';
    });
    ranked.slice().sort(function (a, b) {
      return b.overall - a.overall || b.mergedContributions - a.mergedContributions || a.name.localeCompare(b.name);
    }).forEach(function (c, i) { c.rankOverall = i + 1; });

    ranked.slice().sort(function (a, b) {
      return b.generalist - a.generalist || b.reposTouched - a.reposTouched || a.name.localeCompare(b.name);
    }).forEach(function (c, i) { c.rankGeneralist = i + 1; });

    SKILLS.forEach(function (s) {
      ranked.filter(function (c) {
        return c.skills.some(function (k) { return k.slug === s.slug && k.standing === 'primary'; });
      }).sort(function (a, b) {
        var as = a.skills.filter(function (k) { return k.slug === s.slug; })[0].score;
        var bs = b.skills.filter(function (k) { return k.slug === s.slug; })[0].score;
        return bs - as || b.mergedContributions - a.mergedContributions || a.name.localeCompare(b.name);
      }).forEach(function (c, i) {
        c.skills.filter(function (k) { return k.slug === s.slug; })[0].rank = i + 1;
      });
    });

    CONTRIBUTORS.filter(function (c) { return c.availability.status === 'not_looking'; })
      .forEach(function (c) { c.rankOverall = null; c.rankGeneralist = null; c.unrankedReason = 'opted_out'; });
  }
  assignRanks();

  /* ---- organisation, hirer, shortlists, saved searches ------------------ */
  var ORG = {
    id: 'org-northfield',
    name: 'Northfield Labs',
    initials: 'NF',
    verified: true,
    verifiedAt: '2026-04-18',
    /* Admin-verified to hire; payment capability never verified, so every
       contact request we send carries the ADR-0005 disclosure. */
    paymentVerifiedAt: null,
    members: 4
  };
  var HIRER = { id: 'hirer-maya', name: 'Maya Renner', initials: 'MR', org: ORG.id, hiringEnabled: true };

  function entry(userId, addedDaysAgo, notifiedDaysAgo, state) {
    return {
      userId: userId,
      addedAt: isoDaysAgo(addedDaysAgo),
      addedBy: 'Maya Renner',
      notifiedAt: notifiedDaysAgo == null ? null : isoDaysAgo(notifiedDaysAgo),
      contactStatus: state || null,
      emailReleasedAt: state === 'accepted' ? isoDaysAgo(notifiedDaysAgo - 1) : null,
      email: state === 'accepted' ? (BY_ID[userId].login.replace(/-/g, '.') + '@fastmail.com') : null
    };
  }

  var SHORTLISTS = [
    {
      id: 'sl-platform-q4',
      name: 'Platform team — Q4 backfill',
      description: 'Two senior platform engineers, Go plus a control plane. Panel is booked for the first week of October.',
      status: 'open',
      tentativeResultDate: '2026-09-26',
      createdAt: isoDaysAgo(24),
      createdBy: 'Maya Renner',
      closedAt: null,
      entries: [
        entry('imara-k', 22, 19, 'accepted'),
        entry('sofia-mr', 22, 19, 'accepted'),
        entry('sven-b', 21, 19, 'pending'),
        entry('wei-lin', 20, 19, 'declined'),
        entry('dokafor', 3, null, null)
      ]
    },
    {
      id: 'sl-pg-contract',
      name: 'Postgres performance — 3 month contract',
      description: 'Query planning and lock contention on a 40TB estate. Remote, overlapping European hours.',
      status: 'draft',
      tentativeResultDate: '2026-10-10',
      createdAt: isoDaysAgo(4),
      createdBy: 'Maya Renner',
      closedAt: null,
      entries: [
        entry('kchen', 4, null, null),
        entry('cdiaz', 3, null, null),
        entry('nadia-hr', 1, null, null)
      ]
    },
    {
      id: 'sl-observability',
      name: 'Observability rebuild',
      description: 'Replacing a home-grown metrics pipeline. One senior hire plus a contractor for the migration.',
      status: 'open',
      tentativeResultDate: '2026-08-20',
      createdAt: isoDaysAgo(58),
      createdBy: 'Devon Park',
      closedAt: null,
      entries: [
        entry('priyasr', 55, 52, 'accepted'),
        entry('lgrant', 55, 52, 'pending'),
        entry('sven-b', 54, 52, 'pending'),
        entry('tomasz-l', 54, 52, 'pending')
      ]
    },
    {
      id: 'sl-rust-pilot',
      name: 'Rust ingest pilot',
      description: 'Short pilot, now filled. Kept for the record.',
      status: 'closed',
      tentativeResultDate: '2026-07-15',
      createdAt: isoDaysAgo(96),
      createdBy: 'Maya Renner',
      closedAt: isoDaysAgo(56),
      entries: [
        entry('jonas-w', 94, 90, 'accepted'),
        entry('dmitri-v', 94, 90, 'declined')
      ]
    }
  ];

  var SAVED_SEARCHES = [
    {
      id: 'ss-go-k8s',
      name: 'Go + Kubernetes, ranked depth',
      createdAt: isoDaysAgo(19),
      createdBy: 'Maya Renner',
      filters: { skills: ['go', 'kubernetes'], min_skill_score: 55, min_overall_score: 60, availability: [], include_inactive: false, evidence_within_months: 12, q: '' }
    },
    {
      id: 'ss-pg-freelance',
      name: 'Postgres freelancers, any recency',
      createdAt: isoDaysAgo(11),
      createdBy: 'Devon Park',
      filters: { skills: ['postgres'], min_skill_score: 60, min_overall_score: 0, availability: ['looking_for_freelance', 'open_to_freelance'], include_inactive: true, evidence_within_months: null, q: '' }
    },
    {
      id: 'ss-generalists',
      name: 'Broad generalists, 100+',
      createdAt: isoDaysAgo(6),
      createdBy: 'Maya Renner',
      filters: { skills: [], min_skill_score: 0, min_overall_score: 0, min_generalist_score: 100, availability: [], include_inactive: false, evidence_within_months: null, q: '' }
    }
  ];

  var AVAILABILITY_LABEL = {
    looking_for_job: 'Looking for a job',
    looking_for_freelance: 'Looking for freelance',
    open_to_freelance: 'Employed, open to freelance',
    not_looking: 'Not looking'
  };

  global.GCP = {
    TODAY: TODAY,
    RUBRIC: RUBRIC,
    SKILLS: SKILLS,
    SKILL_BY_SLUG: SKILL_BY_SLUG,
    REPOS: REPOS,
    REPO_BY_SLUG: REPO_BY_SLUG,
    CONTRIBUTORS: CONTRIBUTORS,
    BY_ID: BY_ID,
    ORG: ORG,
    HIRER: HIRER,
    SHORTLISTS: SHORTLISTS,
    SAVED_SEARCHES: SAVED_SEARCHES,
    AVAILABILITY_LABEL: AVAILABILITY_LABEL,
    norm: norm,
    isoDaysAgo: isoDaysAgo
  };
})(window);
