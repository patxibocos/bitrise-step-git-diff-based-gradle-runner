package main

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"runtime"
	"strings"
)

type gradleProject struct {
	name       string
	path       string
	dependants []string
}

func main() {
	projectDir := "C:/Users/oopat/Desktop/MyApplication"
	changedFiles, err := getChangedFilesBetweenBranches(projectDir, "master", "test")
	if err != nil {
		fmt.Println(err)
		panic("Exiting... something went wrong identifying changed files")
	}
	fmt.Printf("Changed files: %d", len(changedFiles))
	_, err = getAllGradleProjects(projectDir)
	if err != nil {
		fmt.Println(err)
		panic("Exiting... could not get Gradle subprojects")
	}
}

func getChangedFilesBetweenBranches(baseDir string, baseBranch string, targetBranch string) ([]string, error) {
	cmd := exec.Command("git", "diff", "--name-only", fmt.Sprintf("%s..%s", baseBranch, targetBranch))
	cmd.Dir = baseDir
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimSuffix(string(output), "\n"), "\n"), nil
}

func getAllGradleProjects(baseDir string) ([]gradleProject, error) {
	gradleBuildScriptFile := findGradleBuildFile(baseDir)
	if gradleBuildScriptFile == nil {
		return nil, errors.New("could not find Gradle build script file")
	}
	err := backupGradleBuildFile(baseDir, *gradleBuildScriptFile)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = restoreGradleBuildFile(baseDir, *gradleBuildScriptFile)
	}()
	err = createIncrementalGradleFile(baseDir)
	if err != nil {
		return nil, err
	}
	err = applyIncrementalGradle(baseDir, *gradleBuildScriptFile)
	if err != nil {
		return nil, err
	}
	err = runGradleIncrementalTask(baseDir)
	if err != nil {
		return nil, err
	}
	return readGradleProjectsFromCsv(baseDir)
}

func backupGradleBuildFile(baseDir string, buildFileName string) error {
	originalBuildFile, err := os.Open(path.Join(baseDir, buildFileName))
	defer func() {
		_ = originalBuildFile.Close()
	}()
	if err != nil {
		return err
	}
	backupFileName := buildFileName + ".bak"
	backupFile, err := os.Create(path.Join(baseDir, backupFileName))
	defer func() {
		_ = backupFile.Close()
	}()
	if err != nil {
		return err
	}
	_, err = io.Copy(backupFile, originalBuildFile)
	if err != nil {
		return err
	}
	return nil

}

func restoreGradleBuildFile(baseDir string, buildFileName string) error {
	err := os.Remove(path.Join(baseDir, buildFileName))
	if err != nil {
		return err
	}
	backupFileName := buildFileName + ".bak"
	return os.Rename(path.Join(baseDir, backupFileName), path.Join(baseDir, buildFileName))
}

func findGradleBuildFile(baseDir string) *string {
	groovyBuildScript := "build.gradle"
	_, err := os.Stat(path.Join(baseDir, groovyBuildScript))
	if !os.IsNotExist(err) {
		return &groovyBuildScript
	}
	kotlinBuildScript := "build.gradle"
	_, err = os.Stat(path.Join(baseDir, kotlinBuildScript))
	if !os.IsNotExist(err) {
		return &kotlinBuildScript
	}
	return nil
}

func applyIncrementalGradle(baseDir string, gradleBuildScriptFile string) error {
	switch gradleBuildScriptFile {
	case "build.gradle":
		groovyBuildScript, err := os.OpenFile(path.Join(baseDir, "build.gradle"), os.O_APPEND|os.O_WRONLY, os.ModeAppend)
		defer func() {
			if groovyBuildScript != nil {
				_ = groovyBuildScript.Close()
			}
		}()
		if err != nil {
			return err
		}
		_, err = groovyBuildScript.WriteString("\napply from: 'incremental.gradle'")
		if err != nil {
			return nil
		}
		return nil
	case "build.gradle.kts":
		kotlinBuildScript, err := os.OpenFile(path.Join(baseDir, "build.gradle.kts"), os.O_APPEND|os.O_WRONLY, os.ModeAppend)
		defer func() {
			if kotlinBuildScript != nil {
				_ = kotlinBuildScript.Close()
			}
		}()
		if err != nil {
			return err
		}
		_, err = kotlinBuildScript.WriteString("\napply(from = \"incremental.gradle\")")
		if err != nil {
			return nil
		}
		return nil
	default:
		return nil
	}
}

func createIncrementalGradleFile(baseDir string) error {
	incrementalFile, err := os.Create(path.Join(baseDir, "incremental.gradle"))
	if err != nil {
		return err
	}
	_, err = incrementalFile.WriteString(`import org.gradle.api.internal.artifacts.dependencies.DefaultProjectDependency

task incremental {
    doLast {
        new File("incremental.csv").withWriter { writer ->
            subprojects.forEach { currentProject ->
                def dependants = subprojects.findAll { subproject ->
                    def implementationDependencies = subproject.configurations.getByName("implementation").dependencies
                    def apiDependencies = subproject.configurations.getByName("api").dependencies
                    (implementationDependencies + apiDependencies).any { dependency ->
                        dependency instanceof DefaultProjectDependency && (dependency as DefaultProjectDependency).dependencyProject == currentProject
                    }
                }.collect { it.name }.join(",")
                writer << "\"$currentProject.name\",\"$currentProject.projectDir.path\",\"$dependants\"\n"
            }
        }
    }
}`)
	err = incrementalFile.Close()
	if err != nil {
		return err
	}
	return nil
}

func runGradleIncrementalTask(baseDir string) error {
	defer func() {
		_ = os.Remove(path.Join(baseDir, "incremental.gradle"))
	}()
	var gradleExecutable string
	if runtime.GOOS == "windows" {
		gradleExecutable = "gradlew.bat"
	} else {
		gradleExecutable = "gradlew"
	}
	cmd := exec.Command(path.Join(baseDir, gradleExecutable), "incremental")
	cmd.Dir = baseDir
	return cmd.Run()
}

func readGradleProjectsFromCsv(baseDir string) ([]gradleProject, error) {
	incrementalCsv, err := os.Open(path.Join(baseDir, "incremental.csv"))
	if err != nil {
		return nil, err
	}
	reader := csv.NewReader(incrementalCsv)
	records, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	var gradleProjects []gradleProject
	for _, row := range records {
		fmt.Printf("App: %s, path: %s", row[0], row[1])
		gradleProjects = append(gradleProjects, gradleProject{
			name:       row[0],
			path:       row[1],
			dependants: strings.Split(row[2], ","),
		})
	}
	_ = incrementalCsv.Close()
	defer func() {
		_ = os.Remove(path.Join(baseDir, "incremental.csv"))
	}()
	return gradleProjects, nil
}