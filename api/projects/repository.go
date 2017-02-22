package projects

import (
	"database/sql"
	"net/http"
	"os"
	"strconv"

	database "github.com/ansible-semaphore/semaphore/db"
	"github.com/ansible-semaphore/semaphore/models"
	"github.com/ansible-semaphore/semaphore/util"
	"github.com/castawaylabs/mulekick"
	"github.com/gorilla/context"
	"github.com/masterminds/squirrel"
)

func clearRepositoryCache(repository models.Repository) error {
	repoName := "repository_" + strconv.Itoa(repository.ID)
	repoPath := util.Config.TmpPath + "/" + repoName
	_, err := os.Stat(repoPath)
	if err == nil {
		return os.RemoveAll(repoPath)
	}
	return nil
}

func RepositoryMiddleware(w http.ResponseWriter, r *http.Request) {
	project := context.Get(r, "project").(models.Project)
	repositoryID, err := util.GetIntParam("repository_id", w, r)
	if err != nil {
		return
	}

	var repository models.Repository
	if err := database.Mysql.SelectOne(&repository, "select * from project__repository where project_id=? and id=?", project.ID, repositoryID); err != nil {
		if err == sql.ErrNoRows {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		panic(err)
	}

	context.Set(r, "repository", repository)
}

func GetRepositories(w http.ResponseWriter, r *http.Request) {
	project := context.Get(r, "project").(models.Project)
	var repos []models.Repository

	query, args, _ := squirrel.Select("*").
		From("project__repository").
		Where("project_id=?", project.ID).
		OrderBy("name asc").
		ToSql()

	if _, err := database.Mysql.Select(&repos, query, args...); err != nil {
		panic(err)
	}

	mulekick.WriteJSON(w, http.StatusOK, repos)
}

func AddRepository(w http.ResponseWriter, r *http.Request) {
	project := context.Get(r, "project").(models.Project)

	var repository struct {
		Name     string `json:"name" binding:"required"`
		GitUrl   string `json:"git_url" binding:"required"`
		SshKeyID int    `json:"ssh_key_id" binding:"required"`
	}
	if err := mulekick.Bind(w, r, &repository); err != nil {
		return
	}

	res, err := database.Mysql.Exec("insert into project__repository set project_id=?, git_url=?, ssh_key_id=?, name=?", project.ID, repository.GitUrl, repository.SshKeyID, repository.Name)
	if err != nil {
		panic(err)
	}

	insertID, _ := res.LastInsertId()
	insertIDInt := int(insertID)
	objType := "repository"

	desc := "Repository (" + repository.GitUrl + ") created"
	if err := (models.Event{
		ProjectID:   &project.ID,
		ObjectType:  &objType,
		ObjectID:    &insertIDInt,
		Description: &desc,
	}.Insert()); err != nil {
		panic(err)
	}

	w.WriteHeader(http.StatusNoContent)
}

func UpdateRepository(w http.ResponseWriter, r *http.Request) {
	oldRepo := context.Get(r, "repository").(models.Repository)
	var repository struct {
		Name     string `json:"name" binding:"required"`
		GitUrl   string `json:"git_url" binding:"required"`
		SshKeyID int    `json:"ssh_key_id" binding:"required"`
	}
	if err := mulekick.Bind(w, r, &repository); err != nil {
		return
	}

	if _, err := database.Mysql.Exec("update project__repository set name=?, git_url=?, ssh_key_id=? where id=?", repository.Name, repository.GitUrl, repository.SshKeyID, oldRepo.ID); err != nil {
		panic(err)
	}

	if oldRepo.GitUrl != repository.GitUrl {
		clearRepositoryCache(oldRepo)
	}

	desc := "Repository (" + repository.GitUrl + ") updated"
	objType := "inventory"
	if err := (models.Event{
		ProjectID:   &oldRepo.ProjectID,
		Description: &desc,
		ObjectID:    &oldRepo.ID,
		ObjectType:  &objType,
	}.Insert()); err != nil {
		panic(err)
	}

	w.WriteHeader(http.StatusNoContent)
}

func RemoveRepository(w http.ResponseWriter, r *http.Request) {
	repository := context.Get(r, "repository").(models.Repository)

	templatesC, err := database.Mysql.SelectInt("select count(1) from project__template where project_id=? and repository_id=?", repository.ProjectID, repository.ID)
	if err != nil {
		panic(err)
	}

	if templatesC > 0 {
		if len(r.URL.Query().Get("setRemoved")) == 0 {
			mulekick.WriteJSON(w, http.StatusBadRequest, map[string]interface{}{
				"error":        "Repository is in use by one or more templates",
				"templatesUse": true,
			})

			return
		}

		if _, err := database.Mysql.Exec("update project__repository set removed=1 where id=?", repository.ID); err != nil {
			panic(err)
		}

		w.WriteHeader(http.StatusNoContent)
		return
	}

	if _, err := database.Mysql.Exec("delete from project__repository where id=?", repository.ID); err != nil {
		panic(err)
	}

	clearRepositoryCache(repository)

	desc := "Repository (" + repository.GitUrl + ") deleted"
	if err := (models.Event{
		ProjectID:   &repository.ProjectID,
		Description: &desc,
	}.Insert()); err != nil {
		panic(err)
	}

	w.WriteHeader(http.StatusNoContent)
}
