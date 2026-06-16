# Benchmarks

Run the small production-shape memory benchmark with:

```sh
npm run bench:small
```

It builds the current source, starts an isolated HTTPS Pappice instance with a
temporary SQLite database, seeds a small support desk, keeps authenticated
customer and staff sessions active, and samples the Pappice process RSS.

This measures server memory, not browser memory. Compare results on the same
machine and with the same options.

Useful options:

```sh
npm run bench:small -- --duration=60s
npm run bench:small -- --customers=12 --staff=3 --tickets-per-customer=5
npm run bench:small -- --json
```
