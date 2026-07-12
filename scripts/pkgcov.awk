# Aggregate a Go cover profile (text format, on stdin) into weighted per-package
# coverage. Emits one TAB-separated row per package plus a final TOTAL row:
#
#     <pkg>\t<covered-stmts>\t<total-stmts>\t<pct>
#     TOTAL\t<covered-stmts>\t<total-stmts>\t<pct>
#
# Statement-weighted (not a mean of per-file ratios), matching `go tool cover`.
# Shared by coverage-report.sh and coverage-gate.sh so the two never disagree.
NR == 1 && /^mode:/ { next }
NF >= 3 {
  path = $1
  sub(/:[0-9].*$/, "", path)   # drop ":startline.col,endline.col"
  sub(/\/[^\/]*$/, "", path)    # drop "/<file>.go" -> package import path
  stmts = $2 + 0
  covered = ($3 + 0) > 0
  tot[path] += stmts
  g_tot += stmts
  if (covered) { cov[path] += stmts; g_cov += stmts }
}
END {
  for (p in tot) {
    pct = tot[p] > 0 ? cov[p] * 100 / tot[p] : 0
    printf "%s\t%d\t%d\t%.1f\n", p, cov[p], tot[p], pct
  }
  gp = g_tot > 0 ? g_cov * 100 / g_tot : 0
  printf "TOTAL\t%d\t%d\t%.1f\n", g_cov, g_tot, gp
}
