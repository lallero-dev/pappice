package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *Store) CreateProject(input CreateProject) (Project, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Project{}, err
	}
	defer tx.Rollback()
	project, err := createProjectTx(tx, input)
	if err != nil {
		return Project{}, err
	}
	if err := tx.Commit(); err != nil {
		return Project{}, err
	}
	return project, nil
}

func (s *Store) ListProjects(user User) []Project {
	var rows *sql.Rows
	var err error
	if normalizeGlobalRole(user.Role) == "admin" {
		rows, err = s.db.Query(`
			SELECT id, key, name, description, 'owner', created_at, updated_at
			FROM projects
			ORDER BY key`)
	} else {
		rows, err = s.db.Query(`
			SELECT p.id, p.key, p.name, p.description, pm.role, p.created_at, p.updated_at
			FROM projects p
			JOIN project_members pm ON pm.project_id = p.id
			WHERE pm.user_id = ?
			ORDER BY p.key`,
			user.ID,
		)
	}
	if err != nil {
		return nil
	}
	defer rows.Close()

	var projects []Project
	for rows.Next() {
		project, err := scanProject(rows)
		if err == nil {
			projects = append(projects, project)
		}
	}
	return projects
}

func (s *Store) GetProject(id int64) (Project, error) {
	row := s.db.QueryRow(`
		SELECT id, key, name, description, '', created_at, updated_at
		FROM projects
		WHERE id = ?`, id)
	project, err := scanProject(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	return project, err
}

func (s *Store) UpdateProject(id int64, patch UpdateProject) (Project, error) {
	project, err := s.GetProject(id)
	if err != nil {
		return Project{}, err
	}
	if patch.Name != nil {
		project.Name = strings.TrimSpace(*patch.Name)
		if project.Name == "" {
			return Project{}, fmt.Errorf("%w: project name is required", ErrValidation)
		}
	}
	if patch.Description != nil {
		project.Description = strings.TrimSpace(*patch.Description)
	}
	project.UpdatedAt = time.Now().UTC()
	_, err = s.db.Exec(
		`UPDATE projects SET name = ?, description = ?, updated_at = ? WHERE id = ?`,
		project.Name, project.Description, formatTime(project.UpdatedAt), id,
	)
	if err != nil {
		return Project{}, err
	}
	return s.GetProject(id)
}

func (s *Store) DeleteProject(id int64) error {
	result, err := s.db.Exec(`DELETE FROM projects WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ProjectRole(userID, projectID int64) (string, bool) {
	var role string
	err := s.db.QueryRow(`SELECT role FROM project_members WHERE user_id = ? AND project_id = ?`, userID, projectID).Scan(&role)
	return normalizeProjectRole(role), err == nil
}

func (s *Store) ListProjectMembers(projectID int64) []ProjectMember {
	rows, err := s.db.Query(`
		SELECT pm.project_id, u.id, u.username, u.display_name, pm.role, pm.created_at
		FROM project_members pm
		JOIN users u ON u.id = pm.user_id
		WHERE pm.project_id = ?
		ORDER BY pm.role, u.username`, projectID)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var members []ProjectMember
	for rows.Next() {
		var member ProjectMember
		var created string
		if err := rows.Scan(&member.ProjectID, &member.UserID, &member.Username, &member.DisplayName, &member.Role, &created); err == nil {
			member.Role = normalizeProjectRole(member.Role)
			member.CreatedAt = parseTime(created)
			members = append(members, member)
		}
	}
	return members
}

func (s *Store) UpsertProjectMember(projectID int64, input UpsertProjectMember) (ProjectMember, error) {
	role := normalizeProjectRole(input.Role)
	if !isValid(validProjectRoles, role) {
		return ProjectMember{}, fmt.Errorf("%w: invalid project role %q", ErrValidation, role)
	}
	if _, err := s.GetProject(projectID); err != nil {
		return ProjectMember{}, err
	}
	user, err := s.GetUser(input.UserID)
	if err != nil {
		return ProjectMember{}, err
	}
	if user.Disabled {
		return ProjectMember{}, fmt.Errorf("%w: user is disabled", ErrValidation)
	}
	now := time.Now().UTC()
	_, err = s.db.Exec(`
		INSERT INTO project_members (project_id, user_id, role, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(project_id, user_id) DO UPDATE SET role = excluded.role`,
		projectID, input.UserID, role, formatTime(now),
	)
	if err != nil {
		return ProjectMember{}, err
	}
	for _, member := range s.ListProjectMembers(projectID) {
		if member.UserID == input.UserID {
			return member, nil
		}
	}
	return ProjectMember{}, ErrNotFound
}

func (s *Store) DeleteProjectMember(projectID, userID int64) error {
	result, err := s.db.Exec(`DELETE FROM project_members WHERE project_id = ? AND user_id = ?`, projectID, userID)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return ErrNotFound
	}
	return nil
}

func createProjectTx(tx *sql.Tx, input CreateProject) (Project, error) {
	key := strings.ToUpper(strings.TrimSpace(input.Key))
	if !projectKeyPattern.MatchString(key) {
		return Project{}, fmt.Errorf("%w: project key must be 2-16 uppercase letters or numbers", ErrValidation)
	}
	name := defaultString(input.Name, key)
	description := strings.TrimSpace(input.Description)
	now := time.Now().UTC()
	result, err := tx.Exec(
		`INSERT INTO projects (key, name, description, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		key, name, description, formatTime(now), formatTime(now),
	)
	if err != nil {
		return Project{}, normalizeSQLError(err)
	}
	id, _ := result.LastInsertId()
	return Project{
		ID:          id,
		Key:         key,
		Name:        name,
		Description: description,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, nil
}

func getProjectTx(tx *sql.Tx, id int64) (Project, error) {
	row := tx.QueryRow(`
		SELECT id, key, name, description, '', created_at, updated_at
		FROM projects WHERE id = ?`, id)
	project, err := scanProject(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	return project, err
}

func scanProject(rows scanner) (Project, error) {
	var project Project
	var created, updated string
	if err := rows.Scan(
		&project.ID, &project.Key, &project.Name, &project.Description, &project.Role,
		&created, &updated,
	); err != nil {
		return Project{}, err
	}
	project.Role = normalizeProjectRole(project.Role)
	project.CreatedAt = parseTime(created)
	project.UpdatedAt = parseTime(updated)
	return project, nil
}
