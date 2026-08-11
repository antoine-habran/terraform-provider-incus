package instance_test

import (
	"fmt"
	"regexp"
	"testing"

	petname "github.com/dustinkirkland/golang-petname"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/lxc/terraform-provider-incus/internal/acctest"
)

func TestAccInstanceDevice_local(t *testing.T) {
	instanceName := petname.Generate(2, "-")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccInstanceDeviceLocal(instanceName),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("incus_instance_device.shared", "instance", instanceName),
					resource.TestCheckResourceAttr("incus_instance_device.shared", "name", "shared"),
					resource.TestCheckResourceAttr("incus_instance_device.shared", "type", "disk"),
					resource.TestCheckResourceAttr("incus_instance_device.shared", "properties.source", "/tmp"),
					resource.TestCheckResourceAttr("incus_instance_device.shared", "properties.path", "/tmp/shared"),
					resource.TestCheckResourceAttr("incus_instance_device.shared", "local", "true"),
				),
			},
			{
				Config: testAccInstanceDeviceLocalUpdated(instanceName),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("incus_instance_device.shared", "properties.path", "/tmp/shared2"),
				),
			},
			{
				Config:             testAccInstanceDeviceLocalUpdated(instanceName),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
			},
			{
				Config:            testAccInstanceDeviceLocalUpdated(instanceName),
				ImportState:       true,
				ImportStateId:     fmt.Sprintf("%s/shared", instanceName),
				ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{
					"local",
				},
			},
		},
	})
}

func TestAccInstanceDevice_conflictingInlineDevice(t *testing.T) {
	instanceName := petname.Generate(2, "-")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccInstanceDeviceLocal(instanceName),
			},
			{
				Config:      testAccInstanceDeviceLocalWithInlineDevice(instanceName),
				ExpectError: regexp.MustCompile(`Refusing to overwrite local device "shared"`),
			},
		},
	})
}

func TestAccInstanceDevice_existingLocalDevice(t *testing.T) {
	instanceName := petname.Generate(2, "-")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccInstanceWithInlineDevice(instanceName),
			},
			{
				Config:      testAccInstanceWithInlineAndIndependentDevice(instanceName),
				ExpectError: regexp.MustCompile(`Instance device already exists locally`),
			},
		},
	})
}

func TestAccInstanceDevice_profileOverride(t *testing.T) {
	profileName := petname.Generate(2, "-")
	instanceName := petname.Generate(2, "-")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccInstanceDeviceProfileOverride(profileName, instanceName),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("incus_instance_device.shared", "type", "disk"),
					resource.TestCheckResourceAttr("incus_instance_device.shared", "properties.path", "/tmp/override"),
					resource.TestCheckResourceAttr("incus_instance_device.shared", "local", "true"),
				),
			},
			{
				Config:             testAccInstanceDeviceProfileOverride(profileName, instanceName),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
			},
		},
	})
}

func testAccInstanceDeviceLocal(instanceName string) string {
	return fmt.Sprintf(`
resource "incus_instance" "test" {
  name     = "%s"
  image    = "%s"
}

resource "incus_instance_device" "shared" {
  instance = incus_instance.test.name
  name     = "shared"
  type     = "disk"

  properties = {
    source = "/tmp"
    path   = "/tmp/shared"
  }

  depends_on = [incus_instance.test]
}
`, instanceName, acctest.TestImage)
}

func testAccInstanceDeviceLocalUpdated(instanceName string) string {
	return fmt.Sprintf(`
resource "incus_instance" "test" {
  name     = "%s"
  image    = "%s"
}

resource "incus_instance_device" "shared" {
  instance = incus_instance.test.name
  name     = "shared"
  type     = "disk"

  properties = {
    source = "/tmp"
    path   = "/tmp/shared2"
  }

  depends_on = [incus_instance.test]
}
`, instanceName, acctest.TestImage)
}

func testAccInstanceDeviceLocalWithInlineDevice(instanceName string) string {
	return fmt.Sprintf(`
resource "incus_instance" "test" {
  name     = "%s"
  image    = "%s"

  device {
    name = "shared"
    type = "disk"

    properties = {
      source = "/tmp"
      path   = "/tmp/shared-inline"
    }
  }
}

resource "incus_instance_device" "shared" {
  instance = incus_instance.test.name
  name     = "shared"
  type     = "disk"

  properties = {
    source = "/tmp"
    path   = "/tmp/shared-independent"
  }

  depends_on = [incus_instance.test]
}
`, instanceName, acctest.TestImage)
}

func testAccInstanceWithInlineDevice(instanceName string) string {
	return fmt.Sprintf(`
resource "incus_instance" "test" {
  name     = "%s"
  image    = "%s"

  device {
    name = "shared"
    type = "disk"

    properties = {
      source = "/tmp"
      path   = "/tmp/shared"
    }
  }
}
`, instanceName, acctest.TestImage)
}

func testAccInstanceWithInlineAndIndependentDevice(instanceName string) string {
	return fmt.Sprintf(`
resource "incus_instance" "test" {
  name     = "%s"
  image    = "%s"

  device {
    name = "shared"
    type = "disk"

    properties = {
      source = "/tmp"
      path   = "/tmp/shared"
    }
  }
}

resource "incus_instance_device" "shared" {
  instance = incus_instance.test.name
  name     = "shared"
  type     = "disk"

  properties = {
    path = "/tmp/independent"
  }

  depends_on = [incus_instance.test]
}
`, instanceName, acctest.TestImage)
}

func testAccInstanceDeviceProfileOverride(profileName, instanceName string) string {
	return fmt.Sprintf(`
resource "incus_profile" "test" {
  name = "%s"

  device {
    name = "shared"
    type = "disk"

    properties = {
      source = "/tmp"
      path   = "/tmp/profile"
    }
  }
}

resource "incus_instance" "test" {
  name     = "%s"
  image    = "%s"
  profiles = ["default", incus_profile.test.name]
}

resource "incus_instance_device" "shared" {
  instance = incus_instance.test.name
  name     = "shared"
  type     = "disk"

  properties = {
    path = "/tmp/override"
  }

  depends_on = [incus_instance.test]
}
`, profileName, instanceName, acctest.TestImage)
}
