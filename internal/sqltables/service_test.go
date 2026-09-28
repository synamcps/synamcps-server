package sqltables

import "testing"

func TestValidateSQL(t *testing.T) {
	if err := ValidateSQL("SELECT * FROM syna_t_abc.data", "syna_t_abc", "data"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSQL("SELECT * FROM other.data; DROP TABLE x", "syna_t_abc", "data"); err == nil {
		t.Fatal("expected reject")
	}
	if err := ValidateSQL("SELECT pg_read_file('/etc/passwd')", "syna_t_abc", "data"); err == nil {
		t.Fatal("expected reject")
	}
	if err := ValidateSQL("UPDATE syna_t_abc.data SET payload='{}'", "syna_t_abc", "data"); err != nil {
		t.Fatal(err)
	}
}
