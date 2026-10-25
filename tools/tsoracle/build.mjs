// Bundles oracle.ts with the esbuild reference/ installs:
//   node tools/tsoracle/build.mjs <reference dir> <outfile>
// The plugin exports the module-private reference functions the oracle calls,
// so they are tested as they are, not copied.
import { createRequire } from 'node:module';
import { readFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const [refDir, outfile] = process.argv.slice(2).map(p => path.resolve(p));
if (!refDir || !outfile) {
	console.error('usage: node build.mjs <reference dir> <outfile>');
	process.exit(2);
}
const esbuild = createRequire(path.join(refDir, 'package.json'))('esbuild');

const privateExports = {
	'src/spec-configuration/containerFeaturesConfiguration.ts': ['escapeQuotesForShell'],
	'src/spec-node/dockerCompose.ts': ['convertMountToVolume', 'convertMountToVolumeTopLevelElement'],
};

const exportPrivate = {
	name: 'export-private',
	setup(build) {
		build.onLoad({ filter: /\.ts$/ }, async args => {
			const names = privateExports[path.relative(refDir, args.path).split(path.sep).join('/')];
			if (!names) {
				return undefined;
			}
			const source = await readFile(args.path, 'utf8');
			return { contents: `${source}\nexport { ${names.join(', ')} };\n`, loader: 'ts' };
		});
	},
};

await esbuild.build({
	entryPoints: [path.join(path.dirname(fileURLToPath(import.meta.url)), 'oracle.ts')],
	bundle: true,
	platform: 'node',
	mainFields: ['module', 'main'],
	outfile,
	logLevel: 'warning',
	nodePaths: [path.join(refDir, 'node_modules')],
	plugins: [exportPrivate],
});
