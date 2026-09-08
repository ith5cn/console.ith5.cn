$ErrorActionPreference = "Stop"

param(
  [string]$Region = "us-east-2",
  [string]$VpcId = "vpc-0abc1234def567890",
  [string]$Project = "console-ith5",
  [int]$AppPort = 8080,
  [switch]$IncludeRdsRule
)

function Invoke-AwsText {
  param([string[]]$AwsArgs)
  $out = & aws @AwsArgs
  if ($LASTEXITCODE -ne 0) {
    throw "aws $($AwsArgs -join ' ') failed"
  }
  return ($out | Out-String).Trim()
}

function Get-OrCreateSecurityGroup {
  param(
    [string]$Name,
    [string]$Description
  )

  $groupId = Invoke-AwsText @(
    "ec2", "describe-security-groups",
    "--region", $Region,
    "--filters", "Name=group-name,Values=$Name", "Name=vpc-id,Values=$VpcId",
    "--query", "SecurityGroups[0].GroupId",
    "--output", "text"
  )

  if ($groupId -and $groupId -ne "None") {
    Write-Host "Exists:  $Name = $groupId"
    return $groupId
  }

  $groupId = Invoke-AwsText @(
    "ec2", "create-security-group",
    "--group-name", $Name,
    "--description", $Description,
    "--vpc-id", $VpcId,
    "--region", $Region,
    "--query", "GroupId",
    "--output", "text"
  )
  Write-Host "Created: $Name = $groupId"
  return $groupId
}

function Add-IngressFromGroup {
  param(
    [string]$GroupId,
    [int]$Port,
    [string]$SourceGroupId,
    [string]$Label
  )

  $permission = "IpProtocol=tcp,FromPort=$Port,ToPort=$Port,UserIdGroupPairs=[{GroupId=$SourceGroupId}]"
  $out = & aws ec2 authorize-security-group-ingress `
    --group-id $GroupId `
    --ip-permissions $permission `
    --region $Region 2>&1

  if ($LASTEXITCODE -eq 0) {
    Write-Host "Added:   $Label"
    return
  }

  if (($out | Out-String) -match "InvalidPermission.Duplicate") {
    Write-Host "Exists:  $Label"
    return
  }

  throw "Failed to add ingress rule: $Label`n$out"
}

$lambdaSg = Get-OrCreateSecurityGroup "${Project}-lambda-sg" "Security group for Lambda proxy functions"
$albSg = Get-OrCreateSecurityGroup "${Project}-alb-sg" "Security group for ALB"
$ecsSg = Get-OrCreateSecurityGroup "${Project}-ecs-sg" "Security group for ECS services"
$rdsSg = Get-OrCreateSecurityGroup "${Project}-rds-sg" "Security group for PostgreSQL RDS"

Add-IngressFromGroup $albSg 80 $lambdaSg "Lambda SG -> ALB SG :80"
Add-IngressFromGroup $ecsSg $AppPort $albSg "ALB SG -> ECS SG :$AppPort"

if ($IncludeRdsRule) {
  Add-IngressFromGroup $rdsSg 5432 $ecsSg "ECS SG -> RDS SG :5432"
}

& aws ec2 create-tags --resources $lambdaSg --tags "Key=Name,Value=${Project}-lambda-sg" "Key=Project,Value=$Project" --region $Region | Out-Null
& aws ec2 create-tags --resources $albSg --tags "Key=Name,Value=${Project}-alb-sg" "Key=Project,Value=$Project" --region $Region | Out-Null
& aws ec2 create-tags --resources $ecsSg --tags "Key=Name,Value=${Project}-ecs-sg" "Key=Project,Value=$Project" --region $Region | Out-Null
& aws ec2 create-tags --resources $rdsSg --tags "Key=Name,Value=${Project}-rds-sg" "Key=Project,Value=$Project" --region $Region | Out-Null

Write-Host ""
Write-Host "Security groups:"
Write-Host "LAMBDA_SG=$lambdaSg"
Write-Host "ALB_SG=$albSg"
Write-Host "ECS_SG=$ecsSg"
Write-Host "RDS_SG=$rdsSg"
Write-Host ""
Write-Host "Traffic model:"
Write-Host "Lambda SG -> ALB SG :80"
Write-Host "ALB SG    -> ECS SG :$AppPort"
if ($IncludeRdsRule) {
  Write-Host "ECS SG    -> RDS SG :5432"
}
