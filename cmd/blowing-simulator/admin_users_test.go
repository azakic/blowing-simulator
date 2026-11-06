package main

import "testing"

func TestValidateRoleOrg(t *testing.T) {
	cases := []struct {
		role, kind string
		fail       bool
	}{
		{RolePartner, OrgKindSubcontractor, false},
		{RolePartner, OrgKindClient, false},
		{RolePartner, "", true},              // partner bez organizacije
		{RolePartner, OrgKindInternal, true}, // partner u internoj firmi
		{RoleAdmin, OrgKindInternal, false},
		{RoleEditor, "", false},
		{RoleViewer, "", false},
		{RoleEditor, OrgKindSubcontractor, true}, // staff u partnerskoj firmi
		{RoleAdmin, OrgKindClient, true},
	}
	for _, c := range cases {
		got := validateRoleOrg(c.role, c.kind)
		if c.fail && got == "" {
			t.Errorf("%s/%q: očekivana greška, dobijeno ok", c.role, c.kind)
		}
		if !c.fail && got != "" {
			t.Errorf("%s/%q: neočekivana greška %q", c.role, c.kind, got)
		}
	}
}

func TestValidUserRole(t *testing.T) {
	for _, r := range []string{"admin", "editor", "partner", "viewer", " ADMIN "} {
		if _, ok := validUserRole(r); !ok {
			t.Errorf("rola %q bi trebalo da bude prihvaćena", r)
		}
	}
	for _, r := range []string{"", "root", "superuser"} {
		if _, ok := validUserRole(r); ok {
			t.Errorf("rola %q ne bi trebalo da prođe", r)
		}
	}
}
