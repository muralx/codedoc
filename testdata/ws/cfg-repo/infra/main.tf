variable "region" {
  default = "us-east-1"
}

locals {
  zone = "${var.region}a"
}

resource "aws_instance" "web" {
  ami               = "ami-123456"
  availability_zone = local.zone
}
