package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ssm"
	"github.com/labstack/gommon/log"
)

var (
	ec2svc *ec2.EC2
	ssmsvc *ssm.SSM
)

const (
	GOLDENIMAGE = "goldenImage"
)

type Ec2BuilderData struct {
	DateCreated  string `json:"dateCreated"`
	OSVersion    string `json:"osVersion"`
	Version      string `json:"version"`
	BuildVersion int    `json:"buildVersion"`
	State        struct {
		Status string `json:"status"`
	} `json:"state"`
	OuputResources struct {
		Amis []struct {
			Image string `json:"image"`
		} `json:"amis"`
	} `json:"outputResources"`
	DistributionConfiguration struct {
		Distrubutions []struct {
			AmiDistributionConfiguration struct {
				AmiTags struct {
					Role    string `json:"role"`
					Project string `json:"project"`
				} `json:"amiTags"`
			} `json:"amiDistributionConfiguration"`
		} `json:"distributions"`
	} `json:"distributionConfiguration"`
}

func getClient(region string) (sess *session.Session, err error) {
	sess, err = session.NewSession(&aws.Config{
		Region: aws.String(region)},
	)

	if err != nil {
		log.Warnf("%s: %+v", "Error creating aws session", err)
		return nil, err
	}

	return sess, nil
}

func HandleRequest(ctx context.Context, snsEvent events.SNSEvent) error {

	var evntByte []byte

	if len(snsEvent.Records) != 1 {
		return fmt.Errorf("got %d sets of records for image, expecting 1", len(snsEvent.Records))
	}

	snsRecord := snsEvent.Records[0].SNS
	evntByte = []byte(snsRecord.Message)

	// parse event data
	var ec2data Ec2BuilderData
	err := json.Unmarshal(evntByte, &ec2data)
	if err != nil {
		return err
	}

	var region = os.Getenv("AWS_REGION")
	sess, err := getClient(region)
	if err != nil {
		return err
	}

	// Add AMI Tags
	ec2svc = ec2.New(sess)
	semanticVersion := fmt.Sprintf("%s/%d", ec2data.Version, ec2data.BuildVersion)
	imageName := ""
	amiParameter := "/ec2ImageBuilder"
	role := ec2data.DistributionConfiguration.Distrubutions[0].AmiDistributionConfiguration.AmiTags.Role
	project := ec2data.DistributionConfiguration.Distrubutions[0].AmiDistributionConfiguration.AmiTags.Project

	if strings.EqualFold(role, GOLDENIMAGE) {
		imageName = fmt.Sprintf("%s-%s", GOLDENIMAGE, semanticVersion)
		amiParameter = fmt.Sprintf("%s/%s", amiParameter, GOLDENIMAGE)
	} else {
		imageName = fmt.Sprintf("%s-%s-%s", project, role, semanticVersion)
		amiParameter = fmt.Sprintf("%s/%s/%s", amiParameter, project, role)
	}

	amiID := ec2data.OuputResources.Amis[0].Image

	// Check if image is available
	if ec2data.State.Status != "AVAILABLE" {
		return fmt.Errorf("The AMI with id [%s] is not available", amiID)
	}

	_, err = ec2svc.CreateTags(&ec2.CreateTagsInput{
		Tags: []*ec2.Tag{
			{
				Key:   aws.String("OS"),
				Value: aws.String(ec2data.OSVersion),
			},
			{
				Key:   aws.String("Version"),
				Value: aws.String(ec2data.Version),
			},
			{
				Key:   aws.String("CostCenter"),
				Value: aws.String("engineering"),
			},
			{
				Key:   aws.String("Date"),
				Value: aws.String(ec2data.DateCreated),
			},
			{
				Key:   aws.String("Name"),
				Value: aws.String(imageName),
			},
		},
		Resources: []*string{aws.String(amiID)},
	})

	if err != nil {
		return err
	}

	// Update ssm parameter store with new AMI ID
	ssmsvc = ssm.New(sess)
	_, err = ssmsvc.PutParameter(&ssm.PutParameterInput{
		Name:      aws.String(amiParameter),
		Overwrite: aws.Bool(true),
		Type:      aws.String("SecureString"),
		Value:     aws.String(amiID),
	})

	if err != nil {
		return err
	}

	return nil

}

func main() {
	lambda.Start(HandleRequest)
}
