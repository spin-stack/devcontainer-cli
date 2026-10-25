import { getContainerFeaturesBaseDockerFile, getFeatureLayers, getFeatureInstallWrapperScript, generateContainerEnvs, getFeatureValueObject, FeaturesConfig } from '../../../../reference/src/spec-configuration/containerFeaturesConfiguration';
import { getDevcontainerMetadataLabel } from '../../../../reference/src/spec-node/imageMetadata';
import { getSafeId } from '../../../../reference/src/spec-node/containerFeatures';

function getFeatureEnvVariables(f: any) {
	const values: any = getFeatureValueObject(f);
	const variables: string[] = [];
	if (values) {
		variables.push(...Object.keys(values).map(name => `${getSafeId(name)}="${values[name]}"`));
	}
	return variables;
}

const input = JSON.parse(require('fs').readFileSync(0, 'utf8'));
const fc: FeaturesConfig = { featureSets: input.featureSets, dstFolder: '/dst' };
const useBK = input.useBuildKit;
const root = useBK ? '.' : '/tmp/build-features/';
const dockerfile = getContainerFeaturesBaseDockerFile(root)
	.replace('#{nonBuildKitFeatureContentFallback}', useBK ? '' : `FROM dev_container_feature_content_temp as dev_containers_feature_content_source`)
	.replace('#{featureLayer}', getFeatureLayers(fc, input.containerUser, input.remoteUser, useBK, root))
	.replace('#{containerEnv}', '')
	.replace('#{devcontainerMetadata}', getDevcontainerMetadataLabel({ config: input.metadata, raw: input.metadata, substitute: (x: any) => x } as any))
	.replace('#{containerEnvMetadata}', generateContainerEnvs(input.configContainerEnv, true));
const files: any = {};
for (const s of fc.featureSets) for (const f of s.features) {
	const vars = getFeatureEnvVariables(f);
	files[f.consecutiveId + '/devcontainer-features.env'] = vars.join('\n');
	files[f.consecutiveId + '/devcontainer-features-install.sh'] = getFeatureInstallWrapperScript(f, s, vars);
}
process.stdout.write(JSON.stringify({ dockerfile, files }));
