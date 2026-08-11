# incus_instance_device

Manages a local device or a local override for an Incus instance device.

This resource is useful when an instance device must be configured after another
resource has been created. For example, an OVN network forward must exist before
setting `ipv4.address.external` on a NIC.

The resource updates only the selected instance device. Other instance devices
and device properties are preserved.

## Example Usage

```hcl
resource "incus_instance" "test" {
  name     = "test-01"
  image    = "images:debian/13/cloud"
  profiles = ["dmz-public"]

  wait_for {
    type = "ipv4"
    nic  = "eth0"
  }

  device {
    name = "root"
    type = "disk"

    properties = {
      pool = "truenas-hdd"
      path = "/"
    }
  }
}

resource "incus_network_forward" "public_ip" {
  network        = "dmz"
  listen_address = "185.50.86.123"

  config = {
    target_address = incus_instance.test.ipv4_address
  }
}

resource "incus_instance_device" "public_ip" {
  instance = incus_instance.test.name
  name     = "eth0"
  type     = "nic"

  properties = {
    "ipv4.address.external" = "185.50.86.123"
  }

  depends_on = [
    incus_network_forward.public_ip,
  ]
}
```

If the named device is inherited from a profile, the resource creates a local
override based on the expanded device. Removing the resource removes that
override and restores the profile device.

An existing local device is not adopted implicitly. Use import to explicitly
manage an existing local device. Do not manage the same device with both
`incus_instance.device` and `incus_instance_device`.

## Argument Reference

* `instance` - **Required** - Name of the instance.
* `name` - **Required** - Name of the device.
* `type` - *Optional* - Device type. It is inferred for existing devices.
* `properties` - **Required** - Device properties managed by this resource.
* `project` - *Optional* - Project where the instance exists.
* `remote` - *Optional* - Remote where the instance exists.

## Importing

Import ID syntax: `[<remote>:][<project>/]<instance-name>/<device-name>`

Example:

```shell
terraform import incus_instance_device.public_ip test-01/eth0
```
