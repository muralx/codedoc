package cfg

import "testing"

func TestHCL(t *testing.T) {
	runCases(t, HCL, []extractCase{
		{name: "terraform", src: `variable "region" {
  default = "us-east-1"
}
variable "name" {}
locals {
  zone = "${var.region}a"
  db_password = "x"
}
resource "aws_instance" "web" {
  ami = "ami-1"
  availability_zone = local.zone
  tags {
    Name = var.name
  }
}
data "aws_ami" "base" {
  owners = ["self"]
}
module "net" {
  cidr = var.cidr[0]
}
output "z" { value = local.zone }
`,
			defs: []KeyDef{
				{"var.region", 1, `"us-east-1"`}, {"var.name", 4, ""},
				{"local.zone", 6, `"${var.region}a"`}, {"local.db_password", 7, "***"},
				{"aws_instance.web.ami", 10, `"ami-1"`}, {"aws_instance.web.availability_zone", 11, "local.zone"},
				{"data.aws_ami.base.owners", 17, `["self"]`}, {"module.net.cidr", 20, "var.cidr[0]"},
			},
			refs: []KeyRef{{"var.region", 6}, {"local.zone", 11}, {"var.name", 13}, {"var.cidr", 20}, {"local.zone", 22}}},
		{name: "top-level attributes", src: "region = \"eu-west-1\"\ncount = 2\n",
			defs: []KeyDef{{"region", 1, `"eu-west-1"`}, {"count", 2, "2"}}},
		{name: "invalid", src: "a = 1\nresource \"x\" {\n", defs: []KeyDef{{"a", 1, "1"}}, wantErr: true},
	})
}

func TestTFVars(t *testing.T) {
	got, err := For(HCL).Extract("env/prod.tfvars", []byte("region = \"eu-west-1\"\ncount = 2\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []KeyDef{{"var.region", 1, `"eu-west-1"`}, {"var.count", 2, "2"}}
	if len(got.Defs) != 2 || got.Defs[0] != want[0] || got.Defs[1] != want[1] {
		t.Errorf("defs %+v, want %+v", got.Defs, want)
	}
}
