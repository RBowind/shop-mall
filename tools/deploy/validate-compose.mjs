import assert from "node:assert/strict";

let input = "";
for await (const chunk of process.stdin) input += chunk;

const config = JSON.parse(input);
const services = config.services ?? {};

function networkNames(serviceName) {
  return Object.keys(services[serviceName]?.networks ?? {}).sort();
}

assert.deepEqual(networkNames("nginx"), ["edge"], "nginx must only use edge");
assert.deepEqual(networkNames("app"), ["backend", "edge"], "app must use edge and backend");
assert.deepEqual(networkNames("postgres"), ["backend"], "postgres must only use backend");
assert.deepEqual(networkNames("migration-postgres"), ["migration"], "migration PostgreSQL must be isolated");
assert.deepEqual(networkNames("migration-test"), ["migration"], "migration test must be isolated");
assert.equal(services.postgres?.ports, undefined, "postgres must not publish a host port");
assert.equal(config.networks?.backend?.internal, true, "backend network must be internal");
assert.equal(config.networks?.migration?.internal, true, "migration network must be internal");

console.log("Compose topology validation passed");
