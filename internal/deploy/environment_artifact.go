package deploy

// An application's build outputs are shared, but each environment chooses its
// deployment driver. Script/SAE targets retain the existing build-type contract.
func environmentArtifactType(sv *Service, target *ServiceEnvironment) string {
	if target != nil {
		switch target.TargetType {
		case "ssh-jar":
			return "jar"
		case "docker-host":
			return "image"
		case "ssh-static":
			return "static"
		case TargetJavaBundle:
			return ArtifactJavaBundle
		}
	}
	return sv.ArtifactType
}
