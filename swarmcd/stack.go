package swarmcd

import (
	"crypto/md5"
	"fmt"
	"os"
	"path"
	"text/template"

	"github.com/docker/cli/cli/command/stack"
	"github.com/go-git/go-git/v5"
	"github.com/goccy/go-yaml"
	"github.com/m-adawi/swarm-cd/util"
)

type swarmStack struct {
	name            string
	repo            *stackRepo
	branch          string
	composePath     string
	sopsFiles       []string
	valuesFile      string
	discoverSecrets bool
}

func newSwarmStack(name string, repo *stackRepo, branch string, composePath string, sopsFiles []string, valuesFile string, discoverSecrets bool) *swarmStack {
	return &swarmStack{
		name:            name,
		repo:            repo,
		branch:          branch,
		composePath:     composePath,
		sopsFiles:       sopsFiles,
		valuesFile:      valuesFile,
		discoverSecrets: discoverSecrets,
	}
}

func (swarmStack *swarmStack) updateStack() (revision string, err error) {
	revision, err = swarmStack.repo.pullChanges(swarmStack.branch)
	if err != nil && err != git.NoErrAlreadyUpToDate {
		return
	}

	stackString, err := swarmStack.readStack()
	if err != nil {
		return
	}

	if swarmStack.valuesFile != "" {
		stackString, err = swarmStack.renderComposeTemplate(stackString)
	}
	if err != nil {
		return
	}

	stackContents, err := swarmStack.parseStackString(stackSting)
	if err != nil {
		return
	}

	err = swarmStack.decryptSopsFiles(stackContents)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt one or more sops files for %s stack: %w", swarmStack.name, err)
	}

	err = swarmStack.rotateConfigsAndSecrets(stackContents)
	if err != nil {
		return
	}

	err = swarmStack.writeStack(stackContents)
	if err != nil {
		return
	}

	err = swarmStack.deployStack()
	return
}

func (swarmStack *swarmStack) readStack() (string, error) {
	// composeFile := path.Join(config.ReposPath, swarmStack.repo.path, swarmStack.composePath)
	composeFile := path.Join(swarmStack.repo.path, swarmStack.composePath)
	composeFileBytes, err := os.ReadFile(composeFile)
	if err != nil {
		return "", fmt.Errorf("could not read compose file %s: %w", composeFile, err)
	}
	return composeFileBytes.String(), nil
}

func (swarmStack *swarmStack) renderComposeTemplate(templateContents string) (string, error) {
	valuesFile := path.Join(config.ReposPath, swarmStack.repo.path, swarmStack.valuesFile)
	valuesBytes, err := os.ReadFile(valuesFile)
	if err != nil {
		return "", fmt.Errorf("could not read %s stack values file: %w", swarmStack.name, err)
	}
	var valuesMap map[string]any
	yaml.Unmarshal(valuesBytes, &valuesMap)
	templ, err := template.New(swarmStack.name).Parse(templateContents)
	if err != nil {
		return "", fmt.Errorf("could not parse %s stack compose file as a Go template: %w", swarmStack.name, err)
	}
	var stackContents bytes.Buffer
	err = templ.Execute(&stackContents, map[string]map[string]any{"Values": valuesMap})
	if err != nil {
		return "", fmt.Errorf("error rending %s stack compose template: %w", swarmStack.name, err)
	}
	return stackContents.String(), nil
}

func (swarmStack *swarmStack) parseStackString(stackString string) (map[string]any, error) {
	var composeMap map[string]any
	err = yaml.Unmarshal(composeFileBytes, &composeMap)
	if err != nil {
		return nil, fmt.Errorf("could not parse yaml file %s: %w", composeFile, err)
	}
	return composeMap, nil
}

func (swarmStack *swarmStack) decryptSopsFiles(composeMap map[string]any) (err error) {
	var sopsFiles []string
	if !swarmStack.discoverSecrets {
		sopsFiles = swarmStack.sopsFiles
	} else {
		if secrets, ok := composeMap["secrets"].(map[string]any); ok {
			for secretName, secret := range secrets {
				secretMap, ok := secret.(map[string]any)
				if !ok {
					return fmt.Errorf("invalid compose file: %s secret must be a map", secretName)
				}
				objectFile, ok := objectMap["file"].(string)
				if !ok {
					return fmt.Errorf("invalid compose file: %s file field must be a string", secretName)
				}
				sopsFiles = append(sopsFiles, objectFile)
			}
		}
	}
	for _, sopsFile := range sopsFiles {
		err = util.DecryptFile(path.Join(swarmStack.repo.path, sopsFile))
		if err != nil {
			return
		}
	}
	return
}

func (swarmStack *swarmStack) rotateConfigsAndSecrets(composeMap map[string]any) error {
	if configs, ok := composeMap["configs"].(map[string]any); ok {
		err = swarmStack.rotateObjects(configs)
		if err != nil {
			return fmt.Errorf("could not rotate one or more config files of stack %s: %w", swarmStack.name, err)
		}
	}
	if secrets, ok := composeMap["secrets"].(map[string]any); ok {
		err = swarmStack.rotateObjects(secrets)
		if err != nil {
			return fmt.Errorf("could not rotate one or more secret files of stack %s: %w", swarmStack.name, err)
		}
	}
	return nil
}

func (swarmStack *swarmStack) rotateObjects(objects map[string]any) error {
	objectsDir := path.Dir(path.Join(swarmStack.repo.path, swarmStack.composePath))
	for objectName, object := range objects {
		objectMap, ok := object.(map[string]any)
		if !ok {
			return fmt.Errorf("invalid compose file: %s object must be a map", objectName)
		}
		objectFile, ok := objectMap["file"].(string)
		if !ok {
			return fmt.Errorf("invalid compose file: %s file field must be a string", objectName)
		}
		objectFilePath := path.Join(objectsDir, objectFile)
		configFileBytes, err := os.ReadFile(objectFilePath)
		if err != nil {
			return fmt.Errorf("could not read file %s for rotation: %w", objectFilePath, err)
		}
		hash := fmt.Sprintf("%x", md5.Sum(configFileBytes))[:8]
		objectMap["name"] = swarmStack.name + "-" + objectName + "-" + hash
	}
	return nil
}

func (swarmStack *starmStack) writeStack(composeMap map[string]any) {
	composeFileBytes, err = yaml.Marshal(composeMap)
	if err != nil {
		return fmt.Errorf("could not store comopse file as yaml after calculating hashes for stack %s", swarmStack.name)
	}
	fileInfo, _ := os.Stat(composeFile)
	os.WriteFile(composeFile, composeFileBytes, fileInfo.Mode())
	return nil
}

func (swarmStack *swarmStack) deployStack() error {
	cmd := stack.NewStackCommand(dockerCli)
	cmd.SetArgs([]string{
		"deploy", "--detach", "--with-registry-auth", "-c",
		path.Join(swarmStack.repo.path, swarmStack.composePath),
		swarmStack.name,
	})
	// To stop printing errors and
	// usage message to stdout
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	err := cmd.Execute()
	if err != nil {
		return fmt.Errorf("could not deploy stack %s: %s", swarmStack.name, err)
	}
	return nil
}
