/*
 * MIT License
 *
 * Copyright (c) 2022-2023 Tochemey
 *
 * Permission is hereby granted, free of charge, to any person obtaining a copy
 * of this software and associated documentation files (the "Software"), to deal
 * in the Software without restriction, including without limitation the rights
 * to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
 * copies of the Software, and to permit persons to whom the Software is
 * furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all
 * copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
 * OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
 * SOFTWARE.
 */

package actors

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/reugn/go-quartz/quartz"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestValidateCronExpression(t *testing.T) {
	timestamp := timestamppb.Now().AsTime()
	// grab the various date part
	second := timestamp.Second()
	minute := timestamp.Minute()
	hour := timestamp.Hour()
	day := timestamp.Day()
	month := timestamp.Month()
	year := timestamp.Year()
	location := timestamp.Location()
	// build the cron expression
	cronExpression := fmt.Sprintf("%s %s %s %s %s %s %s",
		strconv.Itoa(second),
		strconv.Itoa(minute),
		strconv.Itoa(hour),
		strconv.Itoa(day),
		strconv.Itoa(int(month)),
		"?",
		strconv.Itoa(year))
	//cronExpression := "0 0 0 4 JUL ? 2014"
	t.Logf("cron expression=%s", cronExpression)
	trigger, err := quartz.NewCronTriggerWithLoc(cronExpression, location)
	assert.NoError(t, err)
	assert.NotNil(t, trigger)
}
