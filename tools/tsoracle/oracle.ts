// The TypeScript oracle behind internal/**/testdata/ts-oracle/*.json. It reads
// {"cases":[{"fn":"<name>","args":[...]}...]} on stdin and writes the same
// cases on stdout, each with the "result" the real reference function returned
// (undefined becomes null) or the "error" message it threw. An unknown name is
// no case result: the process fails, so a golden never records a typo.
//
// Bundled by build.mjs with reference/'s esbuild; regenerate the goldens with
// `go tool task testdata:ts-oracle` (cmd/devtool ts-oracle).
import { URI } from 'vscode-uri';
import { parseVersion, isEarlierVersion, getEntPasswdShellCommand } from '../../reference/src/spec-common/commonUtils';
import { substitute, beforeContainerSubstitute, containerSubstitute } from '../../reference/src/spec-common/variableSubstitution';
import { extractDockerfile, findBaseImage, findUserStatement, ensureDockerfileHasFinalStageName, supportsBuildContexts } from '../../reference/src/spec-node/dockerfileUtils';
import { mergeConfiguration, getDevcontainerMetadataLabel } from '../../reference/src/spec-node/imageMetadata';
import { generateContainerEnvs, getFeatureValueObject, parseMount, escapeQuotesForShell } from '../../reference/src/spec-configuration/containerFeaturesConfiguration';
import { getSafeId } from '../../reference/src/spec-node/containerFeatures';
import { getFeatureIdWithoutVersion } from '../../reference/src/spec-configuration/containerFeaturesOCI';
import { generateLockfile } from '../../reference/src/spec-configuration/lockfile';
import { getRef, getCollectionRef } from '../../reference/src/spec-configuration/containerCollectionsOCI';
import { replaceTemplatedValues } from '../../reference/src/spec-configuration/containerTemplatesOCI';
import { readVersionPrefix, convertMountToVolume, convertMountToVolumeTopLevelElement } from '../../reference/src/spec-node/dockerCompose';
import { qualifyImageName, isBuildxCacheToInline } from '../../reference/src/spec-node/utils';
import { logEventToTerminalText, createPlainLog } from '../../reference/src/spec-utils/log';
import { parseCrossOriginAuthHosts, isAllowedTokenServiceRealm } from '../../reference/src/spec-configuration/httpOCIRegistry';
import { readLockfile } from '../../reference/src/spec-configuration/lockfile';

// escapeQuotesForShell, convertMountToVolume, convertMountToVolumeTopLevelElement
// and replaceTemplatedValues are module-private in the reference; build.mjs exports them for this bundle.

const nullOutput: any = { write() { }, raw() { }, start() { return 0; }, stop() { }, event() { } };

// A SubstitutionContext as JSON: configFile is a path, made the URI TS carries.
function substitutionContext(c: any) {
	return { ...c, configFile: c.configFile === undefined ? undefined : URI.file(c.configFile) };
}

// A mount string goes through parseMount first, as
// generateFeaturesComposeOverrideContent does with every merged mount.
const asMount = (m: any) => typeof m === 'string' ? parseMount(m) : m;

const fns: Record<string, (...args: any[]) => any> = {
	// spec-common/commonUtils.ts
	parseVersion: (s: string) => parseVersion(s),
	isEarlierVersion: (l: number[], r: number[]) => isEarlierVersion(l, r),
	getEntPasswdShellCommand: (s: string) => getEntPasswdShellCommand(s),
	// spec-common/variableSubstitution.ts
	substitute: (ctx: any, value: any) => substitute(substitutionContext(ctx), value),
	beforeContainerSubstitute: (idLabels: any, value: any) => beforeContainerSubstitute(idLabels ?? undefined, value),
	containerSubstitute: (platform: any, configFile: string | null, env: any, value: any) =>
		containerSubstitute(platform, configFile === null ? undefined : URI.file(configFile), env, value),
	// devcontainerIdForLabels is module-private; ${devcontainerId} is the one way TS reaches it.
	devcontainerIdForLabels: (idLabels: any) => beforeContainerSubstitute(idLabels, { id: '${devcontainerId}' }).id,
	// spec-node/dockerfileUtils.ts: the Dockerfile argument is its text, parsed by extractDockerfile.
	findBaseImage: (df: string, buildArgs: any, target: string | null, platformArgs: any) =>
		findBaseImage(extractDockerfile(df), buildArgs, target ?? undefined, platformArgs),
	findUserStatement: (df: string, buildArgs: any, baseImageEnv: any, platformArgs: any, target: string | null) =>
		findUserStatement(extractDockerfile(df), buildArgs, baseImageEnv, platformArgs, target ?? undefined),
	ensureDockerfileHasFinalStageName: (df: string, name: string) => ensureDockerfileHasFinalStageName(df, name),
	supportsBuildContexts: (df: string) => supportsBuildContexts(extractDockerfile(df)),
	// spec-node/imageMetadata.ts
	mergeConfiguration: (config: any, metadata: any[]) => mergeConfiguration(config, metadata),
	getDevcontainerMetadataLabel: (raw: any[]) => getDevcontainerMetadataLabel({ config: raw, raw, substitute: (x: any) => x }),
	// spec-configuration/containerFeaturesConfiguration.ts
	generateContainerEnvs: (env: any, escapeDollar: boolean) => generateContainerEnvs(env ?? undefined, escapeDollar),
	// The value object's key order is the env file's line order: entries keep it.
	getFeatureValueObject: (feature: any) => Object.entries(getFeatureValueObject(feature)),
	escapeQuotesForShell: (s: string) => escapeQuotesForShell(s),
	// spec-node/containerFeatures.ts
	getSafeId: (s: string) => getSafeId(s),
	// spec-configuration/containerFeaturesOCI.ts
	getFeatureIdWithoutVersion: (s: string) => getFeatureIdWithoutVersion(s),
	// spec-configuration/lockfile.ts: the text writeLockfile writes, which keeps the key order.
	generateLockfileText: async (featuresConfig: any) => JSON.stringify(await generateLockfile(featuresConfig), null, 2) + '\n',
	// spec-configuration/containerCollectionsOCI.ts
	getRef: (s: string) => getRef(nullOutput, s),
	getCollectionRef: (registry: string, namespace: string) => getCollectionRef(nullOutput, registry, namespace),
	// spec-configuration/containerTemplatesOCI.ts
	replaceTemplatedValues: (template: string, options: any) => replaceTemplatedValues(nullOutput, template, options),
	// spec-node/dockerCompose.ts: readVersionPrefix reads the compose files' contents given here.
	readVersionPrefix: async (contents: string[]) => {
		const host: any = { readFile: async (p: string) => Buffer.from(contents[Number(p)], 'utf8') };
		return readVersionPrefix(host, contents.map((_, i) => String(i)));
	},
	convertMountToVolume: (m: any) => convertMountToVolume(asMount(m)),
	convertMountToVolumeTopLevelElement: (m: any) => convertMountToVolumeTopLevelElement(asMount(m)),
	// spec-node/utils.ts
	qualifyImageName: (s: string) => qualifyImageName(s),
	isBuildxCacheToInline: (s: string | null) => isBuildxCacheToInline(s ?? undefined),
	// spec-utils/log.ts: the line the terminal log (stdout a TTY) and the plain
	// log (stdout not a TTY) write for one event, or null for none.
	logEventToTerminalText: (e: any, logLevel: number, startTimestamp: number) => logEventToTerminalText(e, logLevel, startTimestamp),
	plainLogText: (e: any, logLevel: number) => {
		let text: string | null = null;
		createPlainLog(t => { text = t; }, () => logLevel).event(e);
		return text;
	},
	// spec-configuration/httpOCIRegistry.ts: the registry → auth-hosts Map as an
	// object of sorted arrays, so the golden is order-independent.
	parseCrossOriginAuthHosts: (entries: string[]) => Object.fromEntries(
		[...parseCrossOriginAuthHosts(entries)].map(([registry, hosts]) => [registry, [...hosts].sort()])),
	isAllowedTokenServiceRealm: (realm: string, registryUrl: string, entries: string[]) =>
		isAllowedTokenServiceRealm(realm, registryUrl, entries),
	// spec-configuration/lockfile.ts: the devcontainer-lock.json next to a
	// devcontainer.json in a fresh folder holds the case's text, its base64
	// bytes, or is a directory or missing. The result is what the reader made of
	// it: no lockfile (undefined, or null), the empty marker, or a lockfile.
	readLockfile: async (content: string | { base64?: string; directory?: boolean } | null) => {
		const fs = require('fs'), os = require('os'), path = require('path');
		const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'ts-oracle-lockfile-'));
		try {
			const lockfilePath = path.join(dir, 'devcontainer-lock.json');
			if (typeof content === 'string') {
				fs.writeFileSync(lockfilePath, content);
			} else if (content?.base64 !== undefined) {
				fs.writeFileSync(lockfilePath, Buffer.from(content.base64, 'base64'));
			} else if (content?.directory) {
				fs.mkdirSync(lockfilePath);
			}
			const { lockfile, initLockfile } = await readLockfile({ configFilePath: URI.file(path.join(dir, 'devcontainer.json')) } as any);
			return initLockfile ? 'init' : lockfile ? 'lockfile' : 'none';
		} finally {
			fs.rmSync(dir, { recursive: true, force: true });
		}
	},
};

async function main() {
	const input = JSON.parse(require('fs').readFileSync(0, 'utf8'));
	const out: any[] = [];
	for (const c of input.cases) {
		const fn = fns[c.fn];
		if (!fn) {
			throw new Error(`ts-oracle: unknown function ${JSON.stringify(c.fn)}`);
		}
		try {
			const result = await fn(...c.args);
			out.push({ fn: c.fn, args: c.args, result: result === undefined ? null : result });
		} catch (err: any) {
			out.push({ fn: c.fn, args: c.args, error: err?.message || String(err) });
		}
	}
	process.stdout.write(JSON.stringify({ cases: out }));
}

main().catch(err => {
	process.stderr.write(`${err?.stack || err}\n`);
	process.exit(1);
});
