$ErrorActionPreference = "Stop"

param(
  [string]$Region = "us-east-2",
  [string]$VpcId = "vpc-0abc1234def567890",
  [string]$Project = "console-ith5",
  [string]$PublicSubnetId,
  [string]$PrivateSubnetId,
  [string]$NatName = "${Project}-nat",
  [string]$PublicRouteTableName = "${Project}-public-rt",
  [string]$PrivateRouteTableName = "${Project}-private-rt"
)

if (-not $PublicSubnetId) {
  throw "PublicSubnetId is required. Example: -PublicSubnetId subnet-xxxx"
}
if (-not $PrivateSubnetId) {
  throw "PrivateSubnetId is required. Example: -PrivateSubnetId subnet-yyyy"
}

function Invoke-AwsText {
  param([string[]]$AwsArgs)
  $out = & aws @AwsArgs
  if ($LASTEXITCODE -ne 0) {
    throw "aws $($AwsArgs -join ' ') failed"
  }
  return ($out | Out-String).Trim()
}

function Get-AttachedInternetGateway {
  $igwId = Invoke-AwsText @(
    "ec2", "describe-internet-gateways",
    "--region", $Region,
    "--filters", "Name=attachment.vpc-id,Values=$VpcId",
    "--query", "InternetGateways[0].InternetGatewayId",
    "--output", "text"
  )
  if ($igwId -and $igwId -ne "None") {
    Write-Host "Exists:  Internet Gateway = $igwId"
    return $igwId
  }

  $igwId = Invoke-AwsText @(
    "ec2", "create-internet-gateway",
    "--region", $Region,
    "--query", "InternetGateway.InternetGatewayId",
    "--output", "text"
  )
  & aws ec2 attach-internet-gateway --internet-gateway-id $igwId --vpc-id $VpcId --region $Region | Out-Null
  & aws ec2 create-tags --resources $igwId --tags "Key=Name,Value=${Project}-igw" "Key=Project,Value=$Project" --region $Region | Out-Null
  Write-Host "Created: Internet Gateway = $igwId"
  return $igwId
}

function Get-OrCreateRouteTable {
  param([string]$Name)

  $rtbId = Invoke-AwsText @(
    "ec2", "describe-route-tables",
    "--region", $Region,
    "--filters", "Name=vpc-id,Values=$VpcId", "Name=tag:Name,Values=$Name",
    "--query", "RouteTables[0].RouteTableId",
    "--output", "text"
  )
  if ($rtbId -and $rtbId -ne "None") {
    Write-Host "Exists:  Route Table $Name = $rtbId"
    return $rtbId
  }

  $rtbId = Invoke-AwsText @(
    "ec2", "create-route-table",
    "--vpc-id", $VpcId,
    "--region", $Region,
    "--query", "RouteTable.RouteTableId",
    "--output", "text"
  )
  & aws ec2 create-tags --resources $rtbId --tags "Key=Name,Value=$Name" "Key=Project,Value=$Project" --region $Region | Out-Null
  Write-Host "Created: Route Table $Name = $rtbId"
  return $rtbId
}

function Ensure-Route {
  param(
    [string]$RouteTableId,
    [string]$GatewayId,
    [string]$NatGatewayId,
    [string]$Label
  )

  $args = @(
    "ec2", "create-route",
    "--route-table-id", $RouteTableId,
    "--destination-cidr-block", "0.0.0.0/0",
    "--region", $Region
  )
  if ($GatewayId) {
    $args += @("--gateway-id", $GatewayId)
  }
  if ($NatGatewayId) {
    $args += @("--nat-gateway-id", $NatGatewayId)
  }

  $out = & aws @args 2>&1
  if ($LASTEXITCODE -eq 0) {
    Write-Host "Added:   $Label"
    return
  }

  $text = $out | Out-String
  if ($text -match "RouteAlreadyExists") {
    Write-Host "Exists:  $Label"
    return
  }

  throw "Failed to create route: $Label`n$text"
}

function Ensure-SubnetAssociation {
  param(
    [string]$RouteTableId,
    [string]$SubnetId,
    [string]$Label
  )

  $current = Invoke-AwsText @(
    "ec2", "describe-route-tables",
    "--region", $Region,
    "--filters", "Name=association.subnet-id,Values=$SubnetId",
    "--query", "RouteTables[0].Associations[?SubnetId=='$SubnetId'].RouteTableAssociationId | [0]",
    "--output", "text"
  )

  if ($current -and $current -ne "None") {
    & aws ec2 replace-route-table-association --association-id $current --route-table-id $RouteTableId --region $Region | Out-Null
    Write-Host "Updated: $Label"
    return
  }

  & aws ec2 associate-route-table --subnet-id $SubnetId --route-table-id $RouteTableId --region $Region | Out-Null
  Write-Host "Added:   $Label"
}

function Get-OrCreateNatGateway {
  $natId = Invoke-AwsText @(
    "ec2", "describe-nat-gateways",
    "--region", $Region,
    "--filter", "Name=vpc-id,Values=$VpcId", "Name=state,Values=pending,available", "Name=tag:Name,Values=$NatName",
    "--query", "NatGateways[0].NatGatewayId",
    "--output", "text"
  )
  if ($natId -and $natId -ne "None") {
    Write-Host "Exists:  NAT Gateway = $natId"
    return $natId
  }

  $allocationId = Invoke-AwsText @(
    "ec2", "allocate-address",
    "--domain", "vpc",
    "--region", $Region,
    "--query", "AllocationId",
    "--output", "text"
  )

  $natId = Invoke-AwsText @(
    "ec2", "create-nat-gateway",
    "--subnet-id", $PublicSubnetId,
    "--allocation-id", $allocationId,
    "--region", $Region,
    "--query", "NatGateway.NatGatewayId",
    "--output", "text"
  )
  & aws ec2 create-tags --resources $natId --tags "Key=Name,Value=$NatName" "Key=Project,Value=$Project" --region $Region | Out-Null
  Write-Host "Created: NAT Gateway = $natId"
  Write-Host "Waiting for NAT Gateway to become available..."
  & aws ec2 wait nat-gateway-available --nat-gateway-ids $natId --region $Region
  Write-Host "Ready:   NAT Gateway = $natId"
  return $natId
}

$igwId = Get-AttachedInternetGateway
$publicRtbId = Get-OrCreateRouteTable $PublicRouteTableName
$privateRtbId = Get-OrCreateRouteTable $PrivateRouteTableName

Ensure-SubnetAssociation $publicRtbId $PublicSubnetId "public subnet $PublicSubnetId -> $publicRtbId"
Ensure-Route -RouteTableId $publicRtbId -GatewayId $igwId -Label "public route 0.0.0.0/0 -> $igwId"

$natId = Get-OrCreateNatGateway
Ensure-SubnetAssociation $privateRtbId $PrivateSubnetId "private subnet $PrivateSubnetId -> $privateRtbId"
Ensure-Route -RouteTableId $privateRtbId -NatGatewayId $natId -Label "private route 0.0.0.0/0 -> $natId"

Write-Host ""
Write-Host "Networking:"
Write-Host "IGW=$igwId"
Write-Host "NAT=$natId"
Write-Host "PUBLIC_ROUTE_TABLE=$publicRtbId"
Write-Host "PRIVATE_ROUTE_TABLE=$privateRtbId"
Write-Host ""
Write-Host "Expected model:"
Write-Host "Public subnet  -> 0.0.0.0/0 -> Internet Gateway"
Write-Host "Private subnet -> 0.0.0.0/0 -> NAT Gateway"
